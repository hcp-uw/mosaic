package transfer

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// StreamShardToPeer reads a locally stored encrypted shard and forwards its
// chunks to a specific peer using the binary wire protocol. After sending all
// chunks it waits for a ShardStreamAck; if any chunks were missed it retransmits
// only those and loops until the receiver confirms all chunks are present.
// Uses QUIC when a stream can be opened; falls back to UDP otherwise.
//
// The ack channel is registered BEFORE chunks are sent each round so that the
// QUIC-stream-EOF ACK (fired by HandleQUICStreamDone on the receiver) is never
// dropped. On QUIC, ShardStreamDone is omitted because the reliable stream EOF
// is the done signal. On UDP, an explicit ShardStreamDone is still sent.
func StreamShardToPeer(fileHash string, meta *ShardMeta, shardIndex int, peerID string, client *p2p.Client) {
	shardPath := filepath.Join(ShardsDir(), fileHash, fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))
	ackKey := fmt.Sprintf("%s:%d:%s", fileHash, shardIndex, peerID)
	serveStart := time.Now()

	var onlyChunks map[int]struct{} // nil = send all; set on retransmit
	totalChunks := 0

	for {
		// Register ack channel BEFORE sending so the QUIC-EOF ACK is never dropped.
		ackCh := make(chan []int, 1)
		shardAckChans.Store(ackKey, ackCh)

		tc, usedQUIC, ok := streamEncryptedChunks(shardPath, meta, shardIndex, onlyChunks, peerID, client)
		if !ok {
			shardAckChans.Delete(ackKey)
			return
		}
		if totalChunks == 0 {
			totalChunks = tc
		}

		// QUIC path: stream EOF triggers ShardStreamAck via HandleQUICStreamDone.
		// UDP path: send explicit ShardStreamDone to trigger the receiver's ack.
		if !usedQUIC {
			client.SendToPeer(peerID, api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{ //nolint:errcheck
				FileHash:    fileHash,
				ShardIndex:  shardIndex,
				TotalChunks: totalChunks,
			}))
		}

		var missing []int
		select {
		case missing = <-ackCh:
		case <-time.After(30 * time.Second):
			shardAckChans.Delete(ackKey)
			fmt.Printf("[Transfer] Shard %d → %s: ack timeout\n", shardIndex, ShortPeer(peerID))
			return
		}
		shardAckChans.Delete(ackKey)

		if len(missing) == 0 {
			fmt.Printf("[Transfer] Redistributed shard %d of %s → peer %s in %.1fs (%d chunks)\n",
				shardIndex, fileHash[:12], ShortPeer(peerID), time.Since(serveStart).Seconds(), totalChunks)
			return
		}

		fmt.Printf("[Transfer] Shard %d → %s: retransmitting %d/%d missing chunks\n",
			shardIndex, ShortPeer(peerID), len(missing), totalChunks)

		onlyChunks = make(map[int]struct{}, len(missing))
		for _, idx := range missing {
			onlyChunks[idx] = struct{}{}
		}
	}
}

// streamEncryptedChunks reads the on-disk encrypted shard file and sends
// chunks to peerID via QUIC (preferred) or UDP. If onlyChunks is non-nil only
// those chunk indices are sent (skipping others via sequential scan); otherwise
// all chunks are sent. Returns (totalChunks, usedQUIC, success).
func streamEncryptedChunks(shardPath string, meta *ShardMeta, shardIndex int, onlyChunks map[int]struct{}, peerID string, client *p2p.Client) (int, bool, bool) {
	f, err := os.Open(shardPath)
	if err != nil {
		return 0, false, false
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, false, false
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	// The QUIC dial to this peer is asynchronous — it may not be complete when
	// the first shard request arrives. Wait up to 3 s for the connection before
	// falling back to UDP; typical TLS + QUIC handshake takes < 200 ms.
	// Skip the wait entirely if the peer never advertised a QUIC port (e.g.
	// TURN-relayed path where direct QUIC will never succeed).
	var quicStream io.WriteCloser
	var quicErr error
	if client.HasQUICPort(peerID) {
		quicDeadline := time.Now().Add(3 * time.Second)
		for {
			quicStream, quicErr = client.OpenShardStream(peerID)
			if quicErr == nil || time.Now().After(quicDeadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		quicStream, quicErr = client.OpenShardStream(peerID)
	}
	if quicErr == nil {
		defer quicStream.Close()
		fmt.Printf("[Transfer] Shard %d → %s: using QUIC stream\n", shardIndex, ShortPeer(peerID))
	} else {
		fmt.Printf("[Transfer] Shard %d → %s: QUIC unavailable (%v), using UDP\n", shardIndex, ShortPeer(peerID), quicErr)
	}

	// Per-shard pacer for the UDP fallback path. Using a per-goroutine pacer
	// instead of the shared global lets N concurrent shard goroutines each run
	// at full rate rather than sharing a single send-slot queue.
	var localPacer adaptivePacer
	localPacer.interval = pacerInitInterval

	for chunkIdx := 0; chunkIdx < totalChunks; chunkIdx++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			fmt.Printf("[Transfer] streamEncryptedChunks: read chunk %d len failed: %v\n", chunkIdx, err)
			return totalChunks, quicErr == nil, false
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encryptedChunk := make([]byte, n)
		if _, err := io.ReadFull(f, encryptedChunk); err != nil {
			fmt.Printf("[Transfer] streamEncryptedChunks: read chunk %d data failed: %v\n", chunkIdx, err)
			return totalChunks, quicErr == nil, false
		}

		if onlyChunks != nil {
			if _, send := onlyChunks[chunkIdx]; !send {
				continue
			}
		}

		frame, err := encodeBinaryShardChunk(binaryShardChunk{
			fileHash: meta.FileHash,
			// fileName/fileSize deliberately omitted — a peer we serve or redistribute
			// to must not learn our file names or sizes (blind-courier privacy).
			shardIndex:      shardIndex,
			chunkIndex:      chunkIdx,
			totalChunks:     totalChunks,
			totalDataShards: meta.TotalDataShards,
			totalShards:     meta.TotalShards,
			data:            encryptedChunk,
		})
		if err != nil {
			return totalChunks, quicErr == nil, false
		}

		if quicErr == nil {
			if err := sendFrameViaQUIC(quicStream, frame); err != nil {
				fmt.Printf("[Transfer] streamEncryptedChunks: shard %d chunk %d → %s (QUIC) failed: %v\n", shardIndex, chunkIdx, ShortPeer(peerID), err)
				return totalChunks, true, false
			}
		} else {
			localPacer.wait()
			t0 := time.Now()
			err := client.SendRawToPeer(peerID, frame)
			localPacer.adjust(time.Since(t0), err == nil)
			if err != nil {
				fmt.Printf("[Transfer] streamEncryptedChunks: shard %d chunk %d → %s failed: %v\n", shardIndex, chunkIdx, peerID, err)
				return totalChunks, false, false
			}
		}
	}
	return totalChunks, quicErr == nil, true
}

// HandleShardRequest responds to a peer requesting a shard.
//
// If the shard is stored locally it is streamed to the requester using the
// binary chunk protocol (no UDP size limit, same path as redistribution).
//
// If the shard is not local and the request hasn't already been relayed, we
// forward the request to all our other peers. When one of them streams the
// shard back, the relay callback (set by the daemon) forwards it to the
// original requester.
func HandleShardRequest(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardRequestData()
	if err != nil {
		return
	}
	requesterID := msg.Sign.PubKey

	pattern := filepath.Join(ShardsDir(), d.FileHash, fmt.Sprintf("shard%d_*.dat", d.ShardIndex))
	matches, _ := filepath.Glob(pattern)

	if len(matches) > 0 {
		// Fast path: we have it — binary stream to the requester.
		meta := FindShardMetaByHash(d.FileHash)
		if meta == nil {
			fmt.Printf("[Transfer] HandleShardRequest: shard %d of %s found on disk but meta.json missing — cannot serve\n", d.ShardIndex, ShortHash(d.FileHash))
			return
		}
		serveKey := fmt.Sprintf("%s:%d:%s", d.FileHash, d.ShardIndex, requesterID)
		if _, alreadyServing := inProgressServes.LoadOrStore(serveKey, struct{}{}); alreadyServing {
			return // duplicate request — first goroutine already handling it
		}
		fmt.Printf("[Transfer] Serving shard %d of %s → %s\n", d.ShardIndex, ShortHash(d.FileHash), ShortPeer(requesterID))
		go func() {
			defer inProgressServes.Delete(serveKey)
			StreamShardToPeer(d.FileHash, meta, d.ShardIndex, requesterID, client)
		}()
		return
	}

	// Slow path: we don't have it.
	if d.Relayed {
		return
	}

	fmt.Printf("[Transfer] Shard %d of %s not found locally — relaying for %s\n", d.ShardIndex, ShortHash(d.FileHash), ShortPeer(requesterID))
	registerPendingShardRequest(d.FileHash, d.ShardIndex, requesterID)
	relay := api.NewShardRequestMessage(api.NewSignature(client.GetID()), api.ShardRequestData{
		FileHash:   d.FileHash,
		ShardIndex: d.ShardIndex,
		Relayed:    true,
	})
	client.SendToAllPeers(relay) //nolint:errcheck — best-effort
}

// HandleShardStreamDone is called when a peer signals it has finished sending
// all chunks of a shard. We always reply with ShardStreamAck listing any chunk
// indices we did not receive (empty = all present). The sender blocks on this
// reply before proceeding, so it can retransmit missing chunks inline.
func HandleShardStreamDone(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardStreamDoneData()
	if err != nil {
		return
	}
	if d.TotalChunks < 0 || d.TotalChunks > maxChunksPerShard {
		return // implausible chunk count from an untrusted peer — drop
	}
	senderID := msg.Sign.PubKey

	sendAck := func(missing []int) {
		client.SendToPeer(senderID, api.NewShardStreamAckMessage(client.GetID(), api.ShardStreamAckData{ //nolint:errcheck
			FileHash:      d.FileHash,
			ShardIndex:    d.ShardIndex,
			MissingChunks: missing,
		}))
	}

	// If the shard is already on disk, ack success immediately.
	shardPath := filepath.Join(ShardsDir(), d.FileHash,
		fmt.Sprintf("shard%d_%s.dat", d.ShardIndex, d.FileHash))
	if _, err := os.Stat(shardPath); err == nil {
		sendAck(nil)
		return
	}

	key := fmt.Sprintf("%s:%d", d.FileHash, d.ShardIndex)
	assemblyMu.Lock()
	asm, ok := assemblies[key]
	assemblyMu.Unlock()

	if !ok {
		// Check if the assembly completed and finalizeShard is still writing to
		// disk. All chunks are present — don't falsely report them as missing.
		if _, finalizing := finalizingShards.Load(key); finalizing {
			sendAck(nil)
			return
		}
		// No assembly in progress and not finalizing — all chunks are missing.
		missing := make([]int, d.TotalChunks)
		for i := range missing {
			missing[i] = i
		}
		sendAck(missing)
		return
	}

	asm.mu.Lock()
	var missing []int
	for i := 0; i < d.TotalChunks; i++ {
		if _, have := asm.chunks[i]; !have {
			missing = append(missing, i)
		}
	}
	asm.mu.Unlock()

	if len(missing) > 0 {
		fmt.Printf("[Transfer] Shard %d of %s: %d/%d chunks missing — acking for retransmit\n",
			d.ShardIndex, ShortHash(d.FileHash), len(missing), d.TotalChunks)
	}
	sendAck(missing)
}

// HandleQUICStreamDone is called when a QUIC receive stream reaches EOF,
// meaning the sender has reliably delivered all shard chunk frames in order.
// Because callQUICBinaryFrameHandler ran synchronously for each frame before
// this fires, the assembly map is fully populated and we can compute the exact
// missing-chunk list without racing against in-flight HandleBinaryShardChunk calls.
//
// This replaces the UDP ShardStreamDone / ShardStreamAck round-trip on the QUIC
// path, eliminating the race where ShardStreamDone arrived before the last chunk.
func HandleQUICStreamDone(senderID string, lastFrame []byte, client *p2p.Client) {
	chunk, err := decodeBinaryShardChunk(lastFrame)
	if err != nil {
		return
	}

	sendAck := func(missing []int) {
		client.SendToPeer(senderID, api.NewShardStreamAckMessage(client.GetID(), api.ShardStreamAckData{ //nolint:errcheck
			FileHash:      chunk.fileHash,
			ShardIndex:    chunk.shardIndex,
			MissingChunks: missing,
		}))
	}

	// Fast path: shard already written to disk.
	shardPath := filepath.Join(ShardsDir(), chunk.fileHash,
		fmt.Sprintf("shard%d_%s.dat", chunk.shardIndex, chunk.fileHash))
	if _, err := os.Stat(shardPath); err == nil {
		sendAck(nil)
		return
	}

	key := fmt.Sprintf("%s:%d", chunk.fileHash, chunk.shardIndex)

	// Assembly completed and finalizeShard is still writing — all chunks are present.
	if _, finalizing := finalizingShards.Load(key); finalizing {
		sendAck(nil)
		return
	}

	assemblyMu.Lock()
	asm, ok := assemblies[key]
	assemblyMu.Unlock()

	if !ok {
		// No assembly at all — all chunks are missing.
		missing := make([]int, chunk.totalChunks)
		for i := range missing {
			missing[i] = i
		}
		sendAck(missing)
		return
	}

	asm.mu.Lock()
	var missing []int
	for i := 0; i < chunk.totalChunks; i++ {
		if _, have := asm.chunks[i]; !have {
			missing = append(missing, i)
		}
	}
	asm.mu.Unlock()

	if len(missing) > 0 {
		fmt.Printf("[Transfer] Shard %d of %s: %d/%d chunks missing (QUIC EOF ack)\n",
			chunk.shardIndex, ShortHash(chunk.fileHash), len(missing), chunk.totalChunks)
	}
	sendAck(missing)
}

// HandleShardStreamAck is called when the receiver replies to our ShardStreamDone.
// It delivers the missing-chunk list to the channel that StreamShardToPeer or
// sendPlaintextShardToPeer is blocking on.
func HandleShardStreamAck(msg *api.Message, _ *p2p.Client) {
	d, err := msg.GetShardStreamAckData()
	if err != nil {
		return
	}
	// The ack sender is the peer we were pushing chunks to.
	ackKey := fmt.Sprintf("%s:%d:%s", d.FileHash, d.ShardIndex, msg.Sign.PubKey)
	if v, ok := shardAckChans.Load(ackKey); ok {
		ch := v.(chan []int)
		select {
		case ch <- d.MissingChunks:
		default:
			// Channel already has a value; duplicate ack — drop.
		}
	}
}
