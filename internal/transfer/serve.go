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
// chunks to a specific peer using the binary wire protocol. The chunks are
// sent as-is — no decryption or re-encryption needed (blind-courier model).
// Uses QUIC when a stream can be opened; falls back to UDP per-chunk otherwise.
func StreamShardToPeer(fileHash string, meta *ShardMeta, shardIndex int, peerID string, client *p2p.Client) {
	shardPath := filepath.Join(ShardsDir(), fileHash, fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))

	f, err := os.Open(shardPath)
	if err != nil {
		return
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	// Try to open a QUIC stream for reliable, in-order delivery.
	// Fall back to UDP if QUIC isn't established yet.
	quicStream, quicErr := client.OpenShardStream(peerID)
	if quicErr == nil {
		defer quicStream.Close()
	}

	for chunkIdx := 0; chunkIdx < totalChunks; chunkIdx++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			fmt.Printf("[Transfer] StreamShardToPeer: read chunk %d len failed: %v\n", chunkIdx, err)
			return
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encryptedChunk := make([]byte, n)
		if _, err := io.ReadFull(f, encryptedChunk); err != nil {
			fmt.Printf("[Transfer] StreamShardToPeer: read chunk %d data failed: %v\n", chunkIdx, err)
			return
		}

		frame, err := encodeBinaryShardChunk(binaryShardChunk{
			fileHash:        fileHash,
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
			return
		}

		if quicErr == nil {
			if err := sendFrameViaQUIC(quicStream, frame); err != nil {
				fmt.Printf("[Transfer] StreamShardToPeer: shard %d chunk %d → %s (QUIC) failed: %v\n", shardIndex, chunkIdx, peerID[:8], err)
				return
			}
		} else {
			udpPacer.wait()
			t0 := time.Now()
			err := client.SendRawToPeer(peerID, frame)
			udpPacer.adjust(time.Since(t0), err == nil)
			if err != nil {
				fmt.Printf("[Transfer] StreamShardToPeer: shard %d chunk %d → %s failed: %v\n", shardIndex, chunkIdx, peerID, err)
				return
			}
		}
	}
	transport := "UDP"
	if quicErr == nil {
		transport = "QUIC"
	}
	fmt.Printf("[Transfer] Redistributed shard %d of %s → peer %s (%s)\n", shardIndex, fileHash[:12], peerID[:8], transport)

	// Notify the receiver that all chunks have been sent so it can detect
	// any gaps and request selective retransmission.
	doneMsg := api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{
		FileHash:    fileHash,
		ShardIndex:  shardIndex,
		TotalChunks: totalChunks,
	})
	client.SendToPeer(peerID, doneMsg) //nolint:errcheck — best-effort
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
		fmt.Printf("[Transfer] Serving shard %d of %s → %s\n", d.ShardIndex, d.FileHash[:12], requesterID[:8])
		go StreamShardToPeer(d.FileHash, meta, d.ShardIndex, requesterID, client)
		return
	}

	// Slow path: we don't have it.
	if d.Relayed {
		// Already relayed once — stop here to prevent forwarding loops.
		return
	}

	// Register the original requester so the relay callback can forward when the
	// shard arrives, then broadcast a one-hop relay to our own peers.
	fmt.Printf("[Transfer] Shard %d of %s not local — relaying for %s\n", d.ShardIndex, d.FileHash[:12], requesterID[:8])
	registerPendingShardRequest(d.FileHash, d.ShardIndex, requesterID)
	relay := api.NewShardRequestMessage(api.NewSignature(client.GetID()), api.ShardRequestData{
		FileHash:   d.FileHash,
		ShardIndex: d.ShardIndex,
		Relayed:    true,
	})
	client.SendToAllPeers(relay) //nolint:errcheck — best-effort
}

// HandleShardStreamDone is called when a peer signals it has finished sending
// all chunks of a shard. The receiver checks how many chunks it actually has
// and, if any are missing, replies with a ShardChunkMissing message so the
// sender can retransmit only those chunks.
func HandleShardStreamDone(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardStreamDoneData()
	if err != nil {
		return
	}
	senderID := msg.Sign.PubKey
	key := fmt.Sprintf("%s:%d", d.FileHash, d.ShardIndex)

	// If the shard file is already on disk, nothing to do.
	shardPath := filepath.Join(ShardsDir(), d.FileHash,
		fmt.Sprintf("shard%d_%s.dat", d.ShardIndex, d.FileHash))
	if _, err := os.Stat(shardPath); err == nil {
		return
	}

	assemblyMu.Lock()
	asm, ok := assemblies[key]
	assemblyMu.Unlock()

	if !ok {
		// No assembly in progress — request the whole shard via normal path.
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

	if len(missing) == 0 {
		return
	}

	fmt.Printf("[Transfer] Shard %d of %s: %d/%d chunks missing — requesting retransmit\n",
		d.ShardIndex, d.FileHash[:12], len(missing), d.TotalChunks)
	retxMsg := api.NewShardChunkMissingMessage(client.GetID(), api.ShardChunkMissingData{
		FileHash:      d.FileHash,
		ShardIndex:    d.ShardIndex,
		MissingChunks: missing,
	})
	client.SendToPeer(senderID, retxMsg) //nolint:errcheck — best-effort
}

// HandleShardChunkMissing is called when a receiver reports which chunks of a
// shard it did not receive. We re-stream only those specific chunks so the
// receiver can complete its assembly without a full re-request.
func HandleShardChunkMissing(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardChunkMissingData()
	if err != nil {
		return
	}
	if len(d.MissingChunks) == 0 {
		return
	}
	requesterID := msg.Sign.PubKey
	meta := FindShardMetaByHash(d.FileHash)
	if meta == nil {
		return
	}
	fmt.Printf("[Transfer] Retransmitting %d missing chunks of shard %d → %s\n",
		len(d.MissingChunks), d.ShardIndex, requesterID[:8])
	go StreamSpecificChunksToPeer(d.FileHash, meta, d.ShardIndex, d.MissingChunks, requesterID, client)
}

// StreamSpecificChunksToPeer reads a locally stored shard file and re-sends
// only the chunk indices listed in missingChunks to peerID. After sending,
// it transmits another ShardStreamDone so the receiver can verify completion.
func StreamSpecificChunksToPeer(fileHash string, meta *ShardMeta, shardIndex int, missingChunks []int, peerID string, client *p2p.Client) {
	shardPath := filepath.Join(ShardsDir(), fileHash,
		fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))

	f, err := os.Open(shardPath)
	if err != nil {
		return
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	// Build a set for O(1) lookup.
	needed := make(map[int]struct{}, len(missingChunks))
	for _, idx := range missingChunks {
		needed[idx] = struct{}{}
	}

	quicStream, quicErr := client.OpenShardStream(peerID)
	if quicErr == nil {
		defer quicStream.Close()
	}

	for chunkIdx := 0; chunkIdx < totalChunks; chunkIdx++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encryptedChunk := make([]byte, n)
		if _, err := io.ReadFull(f, encryptedChunk); err != nil {
			return
		}

		if _, send := needed[chunkIdx]; !send {
			continue
		}

		frame, err := encodeBinaryShardChunk(binaryShardChunk{
			fileHash:        fileHash,
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
			return
		}

		if quicErr == nil {
			if err := sendFrameViaQUIC(quicStream, frame); err != nil {
				return
			}
		} else {
			udpPacer.wait()
			t0 := time.Now()
			err := client.SendRawToPeer(peerID, frame)
			udpPacer.adjust(time.Since(t0), err == nil)
			if err != nil {
				return
			}
		}
	}

	// Send another ShardStreamDone so the receiver can verify it now has everything.
	doneMsg := api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{
		FileHash:    fileHash,
		ShardIndex:  shardIndex,
		TotalChunks: totalChunks,
	})
	client.SendToPeer(peerID, doneMsg) //nolint:errcheck — best-effort
}
