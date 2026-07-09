package transfer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/encoding"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// UploadFile RS-encodes path, saves shards locally, and streams them to all
// connected peers using the binary wire protocol. Returns the file's SHA-256
// content hash and byte size so the caller can record them in the manifest
// without a second read of the file.
func UploadFile(path string, client *p2p.Client) (fileHash string, fileSize int, err error) {
	// Reset progress immediately so the CLI shows "preparing..." instead of
	// the previous upload's stale 100% while hashing/encoding takes place.
	resetUploadProgress(0)

	filename := filepath.Base(path)
	nameNoExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("cannot stat %s: %w", path, err)
	}
	fileSize = int(info.Size())

	netKey, err := shardEncryptionKey()
	if err != nil {
		return "", 0, fmt.Errorf("cannot derive shard key: %w", err)
	}

	fmt.Printf("[Transfer] Uploading %s  size=%d bytes\n", filename, fileSize)

	// outDir is where the encoder looks for the source file and writes shards.
	// We symlink the source file in rather than copying it to save I/O on large files.
	// On failure (e.g., cross-device), fall back to a full copy.
	outDir, err := os.MkdirTemp("", "mosaic-upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	linkPath := filepath.Join(outDir, filename)
	if err := os.Symlink(path, linkPath); err != nil {
		if err := copyFile(path, linkPath); err != nil {
			return "", 0, fmt.Errorf("cannot stage file: %w", err)
		}
	}

	fmt.Printf("[Transfer] Encoding into %d data + %d parity shards…\n", DataShards, ParityShards)
	enc, err := encoding.NewEncoder(DataShards, ParityShards, outDir, outDir)
	if err != nil {
		return "", 0, fmt.Errorf("encoder init failed: %w", err)
	}
	fileHash, err = enc.EncodeFileAndHash(filename)
	if err != nil {
		return "", 0, fmt.Errorf("encode failed: %w", err)
	}
	fmt.Printf("[Transfer] hash=%s\n", fileHash[:12])

	// Build stable peer order: sort our ID + all connected peer IDs lexicographically.
	// This gives a deterministic shard → node mapping every node can compute independently.
	ourID := ""
	if client != nil {
		ourID = client.GetID()
	}
	var connectedPeers []*p2p.PeerInfo
	if client != nil && client.IsPeerCommunicationAvailable() {
		connectedPeers = client.GetConnectedPeers()
	}
	ids := make([]string, 0, len(connectedPeers)+1)
	if ourID != "" {
		ids = append(ids, ourID)
	}
	for _, p := range connectedPeers {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		ids = []string{ourID}
	}
	numNodes := len(ids)
	ourIndex := 0
	for i, id := range ids {
		if id == ourID {
			ourIndex = i
			break
		}
	}

	// Route each shard: store locally if it maps to us, send to the target peer otherwise.
	shardDir := filepath.Join(ShardsDir(), fileHash)
	_ = os.MkdirAll(shardDir, 0755)

	// Write shard metadata before distributing any shards so that
	// HandleShardRequest can serve ShardRequests that arrive during the
	// upload (triggered by early manifest broadcasts from shardStoredCb).
	writeShardMeta(shardDir, ShardMeta{
		FileName:        filename,
		FileHash:        fileHash,
		FileSize:        fileSize,
		TotalDataShards: DataShards,
		TotalShards:     TotalShards,
		BlockSize:       enc.BlockSize(),
	})

	uploadStart := time.Now()
	resetUploadProgress(TotalShards)

	// Adaptive concurrency: each in-flight shard keeps ~shardSize bytes live in the
	// receiver's assembly map. Cap concurrent streams so total in-flight assembly data
	// stays under 256 MB — full concurrency for small files, sequential-ish for large
	// ones where GC pressure from 860 MB+ of live chunks would kill throughput.
	const assemblyBudget = 256 << 20 // 256 MB
	shardSize := int64(fileSize) / DataShards
	maxConcurrent := TotalShards
	if shardSize > 0 && int64(TotalShards)*shardSize > assemblyBudget {
		maxConcurrent = int(assemblyBudget / shardSize)
		if maxConcurrent < 1 {
			maxConcurrent = 1
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)

	// On machines with enough cores, encrypt all shards in parallel — each shard
	// reads/writes a different file so there's no contention. On constrained
	// machines (droplets with 1-2 vCPUs) encrypt sequentially to avoid CPU
	// thrashing; sends still go out as rate-limited goroutines either way.
	const parallelEncryptThreshold = 4

	if runtime.NumCPU() >= parallelEncryptThreshold {
		var encWg sync.WaitGroup
		for i := 0; i < TotalShards; i++ {
			srcPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", i, nameNoExt))
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			targetIndex := i % numNodes
			isOurs := targetIndex == ourIndex

			encWg.Add(1)
			go func(idx int, src, dst string, ours bool) {
				defer encWg.Done()
				if writeErr := encryptAndStoreShardFile(src, dst, netKey, fileHash, idx); writeErr != nil {
					return
				}
				shardStoredCbMu.Lock()
				cb := shardStoredCb
				shardStoredCbMu.Unlock()
				if cb != nil {
					cb(fileHash, idx)
				}
				if ours {
					uploadShardsDispatched.Add(1)
				}
			}(i, srcPath, dst, isOurs)

			if !isOurs {
				targetPeerID := ids[targetIndex]
				wg.Add(1)
				sem <- struct{}{}
				go func(shardIdx int, peerID string, src string) {
					defer func() { <-sem }()
					defer wg.Done()
					if err := sendPlaintextShardToPeer(src, shardIdx, fileHash, netKey, peerID, client); err != nil {
						fmt.Printf("[Transfer] Shard %d → peer %s failed: %v\n", shardIdx, peerID[:8], err)
					} else {
						shardSentCbMu.Lock()
						cb := shardSentCb
						shardSentCbMu.Unlock()
						if cb != nil {
							cb(fileHash, shardIdx, peerID)
						}
					}
					uploadShardsDispatched.Add(1)
				}(i, targetPeerID, srcPath)
			}
		}
		encWg.Wait()
	} else {
		for i := 0; i < TotalShards; i++ {
			srcPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", i, nameNoExt))
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			targetIndex := i % numNodes
			isOurs := targetIndex == ourIndex

			if writeErr := encryptAndStoreShardFile(srcPath, dst, netKey, fileHash, i); writeErr == nil {
				shardStoredCbMu.Lock()
				cb := shardStoredCb
				shardStoredCbMu.Unlock()
				if cb != nil {
					cb(fileHash, i)
				}
				if isOurs {
					uploadShardsDispatched.Add(1)
				}
			}

			if !isOurs {
				targetPeerID := ids[targetIndex]
				wg.Add(1)
				sem <- struct{}{}
				go func(shardIdx int, peerID string, src string) {
					defer func() { <-sem }()
					defer wg.Done()
					if err := sendPlaintextShardToPeer(src, shardIdx, fileHash, netKey, peerID, client); err != nil {
						fmt.Printf("[Transfer] Shard %d → peer %s failed: %v\n", shardIdx, peerID[:8], err)
					} else {
						shardSentCbMu.Lock()
						cb := shardSentCb
						shardSentCbMu.Unlock()
						if cb != nil {
							cb(fileHash, shardIdx, peerID)
						}
					}
					uploadShardsDispatched.Add(1)
				}(i, targetPeerID, srcPath)
			}
		}
	}
	wg.Wait()

	if len(connectedPeers) == 0 {
		fmt.Println("[Transfer] No peers connected — all shards saved locally")
		return fileHash, fileSize, nil
	}
	elapsed := time.Since(uploadStart)
	sizeMB := float64(fileSize) / (1024 * 1024)
	fmt.Printf("[Transfer] Upload complete: %s (%.1f MB in %.1fs = %.2f MB/s)\n",
		filename, sizeMB, elapsed.Seconds(), sizeMB/elapsed.Seconds())
	return fileHash, fileSize, nil
}

// sendPlaintextShardToPeer reads a plaintext shard file from the RS encoder's
// temp directory, encrypts each chunk, and sends it as binary frames to one peer.
//
// For QUIC: the receiver sends ShardStreamAck when the QUIC stream reaches EOF
// (via HandleQUICStreamDone). This is race-free because QUIC guarantees ordered,
// reliable delivery — the EOF fires only after all chunk frames are received.
// We therefore skip the UDP ShardStreamDone signal on the QUIC path.
//
// For UDP: we send an explicit ShardStreamDone after all chunks and wait for the
// receiver's ShardStreamAck listing any missing chunks. Missing chunks are
// retransmitted and the loop repeats until the receiver reports none missing.
//
// The file name and size are NOT sent in the frames (blind-courier privacy): a
// peer storing shards for us must not learn our file names or sizes. Only the
// owner needs them, and the owner always has them in its local meta.json (written
// at upload time or bootstrapped from the network manifest at download time).
func sendPlaintextShardToPeer(srcPath string, shardIndex int, fileHash string, key [32]byte, peerID string, client *p2p.Client) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat shard %d: %w", shardIndex, err)
	}

	// Open a QUIC stream now (before computing totalChunks) so the first send
	// iteration reuses it. HasQUICConnection() can return true even when
	// OpenUniStreamSync races and fails; opening first avoids that ambiguity.
	firstStream, firstStreamErr := client.OpenShardStream(peerID)

	// Canonical chunking: encrypt with the same 8 KB windows and deterministic
	// per-chunk nonces as encryptAndStoreShardFile, so the peer's stored copy is
	// byte-identical to our local copy. That identity is what storage proofs and
	// repair rely on. The transport (QUIC vs UDP) no longer changes the chunk size;
	// 8 KB stays safely under the 65507-byte UDP datagram limit for the UDP path.
	effChunk := int64(chunkSizeOnDisk)
	totalChunks := int((info.Size() + effChunk - 1) / effChunk)
	ackKey := fmt.Sprintf("%s:%d:%s", fileHash, shardIndex, peerID)
	shardStart := time.Now()

	var currentMissing map[int]struct{} // nil = send all chunks

	// Pass the pre-opened stream into the first iteration so it's used
	// immediately. Retries pass nil and open their own stream inside.
	var preStream io.WriteCloser
	if firstStreamErr == nil {
		preStream = firstStream
	}

	for {
		// Register ack channel BEFORE sending any chunks. On the QUIC path the
		// stream-EOF ACK fires asynchronously after stream.Close(); pre-registering
		// ensures we never miss it even on very fast local connections.
		ackCh := make(chan []int, 1)
		shardAckChans.Store(ackKey, ackCh)

		usedQUIC, serr := sendPlaintextChunks(srcPath, currentMissing, shardIndex, fileHash, totalChunks, int(effChunk), key, peerID, client, preStream)
		preStream = nil // consumed; retries open their own stream
		if serr != nil {
			shardAckChans.Delete(ackKey)
			return serr
		}

		// QUIC path: HandleQUICStreamDone on the receiver side sends ShardStreamAck
		// on stream EOF — no UDP signal needed and no risk of arriving too early.
		// UDP path: send explicit ShardStreamDone to trigger the receiver's ack.
		//
		// Zero-chunk shard (an empty file): QUIC sends no frames, so the stream-EOF
		// ACK can't fire (there's no frame to identify the shard). Send an explicit
		// ShardStreamDone in that case too, so the receiver acks immediately instead
		// of the sender hanging for the full ack timeout.
		if !usedQUIC || totalChunks == 0 {
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
			return fmt.Errorf("ack timeout for shard %d → %s", shardIndex, shortPeer(peerID))
		}
		shardAckChans.Delete(ackKey)

		if len(missing) == 0 {
			if !usedQUIC {
				udpPacer.ackSuccess()
			}
			elapsed := time.Since(shardStart)
			sizeMB := float64(info.Size()) / (1024 * 1024)
			fmt.Printf("[Transfer] Shard %d → %s confirmed (%d chunks, %.1fs, %.2f MB/s)\n",
				shardIndex, shortPeer(peerID), totalChunks, elapsed.Seconds(), sizeMB/elapsed.Seconds())
			return nil
		}

		if !usedQUIC {
			udpPacer.ackLoss(float64(len(missing)) / float64(totalChunks))
		}
		if currentMissing == nil {
			fmt.Printf("[Transfer] Shard %d → %s: retransmitting %d/%d missing chunks\n",
				shardIndex, shortPeer(peerID), len(missing), totalChunks)
		} else {
			fmt.Printf("[Transfer] Shard %d → %s: still missing %d/%d chunks\n",
				shardIndex, shortPeer(peerID), len(missing), totalChunks)
		}

		currentMissing = make(map[int]struct{}, len(missing))
		for _, idx := range missing {
			currentMissing[idx] = struct{}{}
		}
	}
}

// sendPlaintextChunks opens srcPath and sends encrypted chunks as binary frames.
// effectiveChunkSize controls read granularity: 8 KB for UDP (limits IP
// fragmentation), 256 KB for QUIC (reduces per-shard frame count ~32×).
// If onlyChunks is non-nil, only those indices are sent (seeks directly to each
// for O(missing) disk I/O); otherwise all chunks are sent in order.
// preOpenedStream, if non-nil, is used directly (and closed on return) instead
// of opening a new QUIC stream; pass nil for retransmit iterations.
// Returns (usedQUIC, error) so the caller can decide whether to send ShardStreamDone.
func sendPlaintextChunks(srcPath string, onlyChunks map[int]struct{}, shardIndex int, fileHash string, totalChunks, effectiveChunkSize int, key [32]byte, peerID string, client *p2p.Client, preOpenedStream io.WriteCloser) (bool, error) {
	sf, err := os.Open(srcPath)
	if err != nil {
		return false, fmt.Errorf("open shard %d: %w", shardIndex, err)
	}
	defer sf.Close()

	var quicStream io.WriteCloser
	if preOpenedStream != nil {
		quicStream = preOpenedStream
		defer quicStream.Close()
	} else {
		qs, qerr := client.OpenShardStream(peerID)
		if qerr == nil {
			quicStream = qs
			defer quicStream.Close()
		}
	}
	usedQUIC := quicStream != nil

	sendOne := func(plaintext []byte, chunkIndex int) error {
		// Deterministic nonce keyed by (fileHash, shardIndex, chunkIndex) so the
		// ciphertext the peer stores is byte-identical to our local on-disk copy.
		encrypted, err := encryptChunkDeterministic(key, fileHash, shardIndex, chunkIndex, plaintext)
		if err != nil {
			return fmt.Errorf("encrypt shard %d chunk %d: %w", shardIndex, chunkIndex, err)
		}
		frame, err := encodeBinaryShardChunk(binaryShardChunk{
			fileHash: fileHash,
			// fileName/fileSize deliberately omitted — peers must not learn them.
			shardIndex:      shardIndex,
			chunkIndex:      chunkIndex,
			totalChunks:     totalChunks,
			totalDataShards: DataShards,
			totalShards:     TotalShards,
			data:            encrypted,
		})
		if err != nil {
			return fmt.Errorf("encode frame shard %d chunk %d: %w", shardIndex, chunkIndex, err)
		}
		if usedQUIC {
			if werr := sendFrameViaQUIC(quicStream, frame); werr != nil {
				return fmt.Errorf("QUIC send shard %d chunk %d: %w", shardIndex, chunkIndex, werr)
			}
		} else {
			udpPacer.wait()
			t0 := time.Now()
			werr := client.SendRawToPeer(peerID, frame)
			udpPacer.adjust(time.Since(t0), werr == nil)
			if werr != nil {
				return fmt.Errorf("UDP send shard %d chunk %d: %w", shardIndex, chunkIndex, werr)
			}
		}
		return nil
	}

	buf := make([]byte, effectiveChunkSize)

	if onlyChunks != nil {
		// Retransmit: seek to each missing chunk directly for efficiency.
		sorted := make([]int, 0, len(onlyChunks))
		for idx := range onlyChunks {
			sorted = append(sorted, idx)
		}
		sort.Ints(sorted)
		for _, chunkIndex := range sorted {
			if _, err := sf.Seek(int64(chunkIndex)*int64(effectiveChunkSize), io.SeekStart); err != nil {
				return usedQUIC, fmt.Errorf("seek shard %d chunk %d: %w", shardIndex, chunkIndex, err)
			}
			n, err := io.ReadFull(sf, buf)
			if n > 0 {
				if serr := sendOne(buf[:n], chunkIndex); serr != nil {
					return usedQUIC, serr
				}
			}
			if err != nil && err != io.ErrUnexpectedEOF {
				return usedQUIC, fmt.Errorf("read shard %d chunk %d: %w", shardIndex, chunkIndex, err)
			}
		}
		return usedQUIC, nil
	}

	// Full send: sequential read.
	for chunkIndex := 0; ; chunkIndex++ {
		n, err := io.ReadFull(sf, buf)
		if n > 0 {
			if serr := sendOne(buf[:n], chunkIndex); serr != nil {
				return usedQUIC, serr
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return usedQUIC, nil
		}
		if err != nil {
			return usedQUIC, fmt.Errorf("read shard %d: %w", shardIndex, err)
		}
	}
}
