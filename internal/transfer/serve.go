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
// Returns true if the receiver acknowledged all chunks, false on ack timeout.
func StreamShardToPeer(fileHash string, meta *ShardMeta, shardIndex int, peerID string, client *p2p.Client) bool {
	shardPath := filepath.Join(ShardsDir(), fileHash, fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))

	totalChunks, ok := streamEncryptedChunks(shardPath, meta, shardIndex, nil, peerID, client)
	if !ok {
		return false
	}

	// Use ShardStreamDone/ShardStreamAck to confirm delivery and retransmit any
	// missing chunks. ShardStreamDone is retried every 2s in case of UDP packet loss.
	ackKey := fmt.Sprintf("%s:%d:%s", fileHash, shardIndex, peerID)
	doneMsg := api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{
		FileHash:    fileHash,
		ShardIndex:  shardIndex,
		TotalChunks: totalChunks,
	})

	for {
		ackCh := make(chan []int, 1)
		shardAckChans.Store(ackKey, ackCh)

		client.SendToPeer(peerID, doneMsg) //nolint:errcheck

		retryTick := time.NewTicker(2 * time.Second)
		deadline := time.NewTimer(15 * time.Second)
		var missing []int
		var timedOut bool
	waitAck:
		for {
			select {
			case missing = <-ackCh:
				break waitAck
			case <-retryTick.C:
				client.SendToPeer(peerID, doneMsg) //nolint:errcheck
			case <-deadline.C:
				timedOut = true
				break waitAck
			}
		}
		retryTick.Stop()
		deadline.Stop()
		shardAckChans.Delete(ackKey)

		if timedOut {
			fmt.Printf("[Transfer] Shard %d → %s: ack timeout\n", shardIndex, shortPeer(peerID))
			return false
		}

		if len(missing) == 0 {
			fmt.Printf("[Transfer] Redistributed shard %d of %s → %s (%d chunks)\n",
				shardIndex, fileHash[:12], shortPeer(peerID), totalChunks)
			return true
		}

		fmt.Printf("[Transfer] Shard %d → %s: retransmitting %d/%d missing chunks\n",
			shardIndex, shortPeer(peerID), len(missing), totalChunks)

		needed := make(map[int]struct{}, len(missing))
		for _, idx := range missing {
			needed[idx] = struct{}{}
		}
		if _, ok := streamEncryptedChunks(shardPath, meta, shardIndex, needed, peerID, client); !ok {
			return false
		}
	}
}

// streamEncryptedChunks reads the on-disk encrypted shard file and sends
// chunks to peerID via QUIC (preferred) or UDP. If onlyChunks is non-nil only
// those chunk indices are sent (skipping others via sequential scan); otherwise
// all chunks are sent. Returns (totalChunks, success).
func streamEncryptedChunks(shardPath string, meta *ShardMeta, shardIndex int, onlyChunks map[int]struct{}, peerID string, client *p2p.Client) (int, bool) {
	f, err := os.Open(shardPath)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, false
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	quicStream, quicErr := client.OpenShardStream(peerID)
	if quicErr == nil {
		defer quicStream.Close()
	}
	usedQUIC := quicErr == nil
	if !usedQUIC && client.IsForceQUIC() {
		fmt.Printf("[Transfer] streamEncryptedChunks: QUIC required but unavailable for shard %d → %s: %v\n", shardIndex, shortPeer(peerID), quicErr)
		return 0, false
	}

	// writeQUICDone appends a ShardStreamDone JSON frame as the last frame on
	// the QUIC stream. QUIC guarantees in-order delivery within a stream, so
	// the receiver always processes this AFTER every chunk frame — eliminating
	// the timing race where a UDP ShardStreamDone arrives before QUIC chunk data.
	writeQUICDone := func() {
		if !usedQUIC {
			return
		}
		doneMsg := api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{
			FileHash:    meta.FileHash,
			ShardIndex:  shardIndex,
			TotalChunks: totalChunks,
		})
		if doneData, serErr := doneMsg.Serialize(); serErr == nil {
			sendFrameViaQUIC(quicStream, doneData) //nolint:errcheck — best-effort; UDP ShardStreamDone is also sent
		}
	}

	for chunkIdx := 0; chunkIdx < totalChunks; chunkIdx++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			fmt.Printf("[Transfer] streamEncryptedChunks: read chunk %d len failed: %v\n", chunkIdx, err)
			return totalChunks, false
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encryptedChunk := make([]byte, n)
		if _, err := io.ReadFull(f, encryptedChunk); err != nil {
			fmt.Printf("[Transfer] streamEncryptedChunks: read chunk %d data failed: %v\n", chunkIdx, err)
			return totalChunks, false
		}

		if onlyChunks != nil {
			if _, send := onlyChunks[chunkIdx]; !send {
				continue
			}
		}

		frame, err := encodeBinaryShardChunk(binaryShardChunk{
			fileHash:        meta.FileHash,
			fileName:        meta.FileName,
			fileSize:        meta.FileSize,
			shardIndex:      shardIndex,
			chunkIndex:      chunkIdx,
			totalChunks:     totalChunks,
			totalDataShards: meta.TotalDataShards,
			totalShards:     meta.TotalShards,
			data:            encryptedChunk,
		})
		if err != nil {
			return totalChunks, false
		}

		if usedQUIC {
			if err := sendFrameViaQUIC(quicStream, frame); err != nil {
				fmt.Printf("[Transfer] streamEncryptedChunks: shard %d chunk %d → %s (QUIC) failed: %v\n", shardIndex, chunkIdx, shortPeer(peerID), err)
				return totalChunks, false
			}
		} else {
			udpPacer.wait()
			t0 := time.Now()
			err := client.SendRawToPeer(peerID, frame)
			udpPacer.adjust(time.Since(t0), err == nil)
			if err != nil {
				fmt.Printf("[Transfer] streamEncryptedChunks: shard %d chunk %d → %s failed: %v\n", shardIndex, chunkIdx, peerID, err)
				return totalChunks, false
			}
		}
	}
	writeQUICDone()
	return totalChunks, true
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
			return
		}
		serveKey := fmt.Sprintf("%s:%d:%s", d.FileHash, d.ShardIndex, requesterID)
		if _, loaded := inProgressServes.LoadOrStore(serveKey, struct{}{}); loaded {
			// Already serving this shard to this peer — drop duplicate request.
			return
		}
		fmt.Printf("[Transfer] Serving shard %d of %s → %s\n", d.ShardIndex, d.FileHash[:12], shortPeer(requesterID))
		go func() {
			defer inProgressServes.Delete(serveKey)
			StreamShardToPeer(d.FileHash, meta, d.ShardIndex, requesterID, client)
		}()
		return
	}

	// Slow path: we don't have it.
	if d.Relayed {
		// Already relayed once — stop here to prevent forwarding loops.
		return
	}

	// Register the original requester so the relay callback can forward when the
	// shard arrives, then broadcast a one-hop relay to our own peers.
	fmt.Printf("[Transfer] Shard %d of %s not local — relaying for %s\n", d.ShardIndex, d.FileHash[:12], shortPeer(requesterID))
	registerPendingShardRequest(d.FileHash, d.ShardIndex, requesterID)
	relay := api.NewShardRequestMessage(api.NewSignature(client.GetID()), api.ShardRequestData{
		FileHash:   d.FileHash,
		ShardIndex: d.ShardIndex,
		Relayed:    true,
	})
	client.SendToAllPeers(relay) //nolint:errcheck — best-effort
}

// HandleShardStreamDone is called when a peer signals it has finished sending
// all chunks of a shard. We reply with ShardStreamAck listing any chunk
// indices we did not receive (empty = all present). The sender blocks on this
// reply before proceeding, so it can retransmit missing chunks inline.
//
// ShardStreamDone travels on the UDP control channel and can arrive before all
// QUIC stream frames have propagated through the OS networking stack to this
// goroutine. We poll briefly to let those frames settle before computing the
// missing list, which prevents spurious retransmits on QUIC paths.
func HandleShardStreamDone(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardStreamDoneData()
	if err != nil {
		return
	}
	senderID := msg.Sign.PubKey

	sendAck := func(missing []int) {
		client.SendToPeer(senderID, api.NewShardStreamAckMessage(client.GetID(), api.ShardStreamAckData{ //nolint:errcheck
			FileHash:      d.FileHash,
			ShardIndex:    d.ShardIndex,
			MissingChunks: missing,
		}))
	}

	shardPath := filepath.Join(ShardsDir(), d.FileHash,
		fmt.Sprintf("shard%d_%s.dat", d.ShardIndex, d.FileHash))
	key := fmt.Sprintf("%s:%d", d.FileHash, d.ShardIndex)

	// Poll up to 2 s for QUIC frames that are still in-flight through the
	// networking stack. Each iteration checks whether the shard is already
	// complete (on disk, fully assembled, or being finalized) so we ack immediately.
	// 2 s rather than the old 200 ms because the QUIC in-stream done sentinel
	// arrives in-order after all chunk frames, but the goroutines that store
	// chunks (go HandleBinaryShardChunk) are scheduled asynchronously — on a
	// busy receiver the last-chunk goroutine can lag the done-sentinel goroutine
	// by more than 200 ms.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(shardPath); statErr == nil {
			sendAck(nil)
			return
		}
		if _, ok := finalizingShards.Load(key); ok {
			sendAck(nil) // all chunks present; finalizeShard is writing the file
			return
		}
		assemblyMu.Lock()
		asm, asmOK := assemblies[key]
		assemblyMu.Unlock()
		if asmOK {
			asm.mu.Lock()
			have := len(asm.chunks)
			asm.mu.Unlock()
			if have >= d.TotalChunks {
				sendAck(nil) // all chunks present; finalizeShard is in flight
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Drain timeout reached — compute what is actually missing and request retransmit.
	if _, statErr := os.Stat(shardPath); statErr == nil {
		sendAck(nil)
		return
	}
	// Shard is being written to disk right now — all chunks present.
	if _, ok := finalizingShards.Load(key); ok {
		sendAck(nil)
		return
	}

	assemblyMu.Lock()
	asm, ok := assemblies[key]
	assemblyMu.Unlock()

	if !ok {
		// No assembly and not finalizing: all chunks are genuinely missing.
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
			d.ShardIndex, d.FileHash[:12], len(missing), d.TotalChunks)
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
