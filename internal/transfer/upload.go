package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("cannot open %s: %w", path, err)
	}
	hasher := sha256.New()
	fileSize64, err := io.Copy(hasher, f)
	f.Close()
	if err != nil {
		return "", 0, fmt.Errorf("cannot hash %s: %w", path, err)
	}
	fileHash = hex.EncodeToString(hasher.Sum(nil))
	fileSize = int(fileSize64)

	netKey, err := shardEncryptionKey()
	if err != nil {
		return "", 0, fmt.Errorf("cannot derive shard key: %w", err)
	}

	fmt.Printf("[Transfer] Uploading %s  hash=%s…  size=%d bytes\n", filename, fileHash[:12], fileSize)

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
	if err := enc.EncodeFile(filename); err != nil {
		return "", 0, fmt.Errorf("encode failed: %w", err)
	}

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

	resetUploadProgress(TotalShards)

	// Allow one goroutine per distinct peer target so sends to different peers
	// run concurrently. Sends to the same peer are still ordered within the goroutine.
	numPeerTargets := numNodes - 1
	if numPeerTargets < 1 {
		numPeerTargets = 1
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, numPeerTargets)

	for i := 0; i < TotalShards; i++ {
		srcPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", i, nameNoExt))
		targetIndex := i % numNodes

		if targetIndex == ourIndex {
			// Our shard: encrypt and persist locally, register in ShardMap.
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			if writeErr := encryptAndStoreShardFile(srcPath, dst, netKey); writeErr == nil {
				uploadShardsDispatched.Add(1)
				shardStoredCbMu.Lock()
				cb := shardStoredCb
				shardStoredCbMu.Unlock()
				if cb != nil {
					cb(fileHash, i)
				}
			}
		} else {
			// Peer's shard: also store an encrypted copy locally so that
			// HandleShardRequest can re-serve it if the push fails or QUIC
			// drops chunks mid-stream.
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			if writeErr := encryptAndStoreShardFile(srcPath, dst, netKey); writeErr == nil {
				shardStoredCbMu.Lock()
				cb := shardStoredCb
				shardStoredCbMu.Unlock()
				if cb != nil {
					cb(fileHash, i)
				}
			}

			// Push directly to the peer in a rate-limited goroutine.
			targetPeerID := ids[targetIndex]
			wg.Add(1)
			sem <- struct{}{}
			go func(shardIdx int, peerID string, src string) {
				defer func() { <-sem }()
				defer wg.Done()
				if err := sendPlaintextShardToPeer(src, shardIdx, fileHash, filename, fileSize, netKey, peerID, client); err != nil {
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
	wg.Wait()

	if len(connectedPeers) == 0 {
		fmt.Println("[Transfer] No peers connected — all shards saved locally")
		return fileHash, fileSize, nil
	}
	fmt.Printf("[Transfer] Upload complete: %s\n", filename)
	return fileHash, fileSize, nil
}

// sendPlaintextShardToPeer reads a plaintext shard file from the RS encoder's
// temp directory, encrypts each chunk, and sends it as binary frames to one peer.
// Used during upload so peer-bound shards are never written to the uploader's disk.
// Uses QUIC when a stream can be opened; falls back to UDP otherwise.
func sendPlaintextShardToPeer(srcPath string, shardIndex int, fileHash, fileName string, fileSize int, key [32]byte, peerID string, client *p2p.Client) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat shard %d: %w", shardIndex, err)
	}
	totalChunks := int((info.Size() + chunkSize - 1) / chunkSize)

	sf, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open shard %d: %w", shardIndex, err)
	}
	defer sf.Close()

	quicStream, quicErr := client.OpenShardStream(peerID)
	if quicErr == nil {
		defer quicStream.Close()
	}

	buf := make([]byte, chunkSize)
	for chunkIndex := 0; ; chunkIndex++ {
		n, err := io.ReadFull(sf, buf)
		if n > 0 {
			encrypted, eerr := encryptChunk(key, buf[:n])
			if eerr != nil {
				return fmt.Errorf("encrypt shard %d chunk %d: %w", shardIndex, chunkIndex, eerr)
			}
			frame, ferr := encodeBinaryShardChunk(binaryShardChunk{
				fileHash:        fileHash,
				fileName:        fileName,
				fileSize:        fileSize,
				shardIndex:      shardIndex,
				chunkIndex:      chunkIndex,
				totalChunks:     totalChunks,
				totalDataShards: DataShards,
				totalShards:     TotalShards,
				data:            encrypted,
			})
			if ferr != nil {
				return fmt.Errorf("encode frame shard %d chunk %d: %w", shardIndex, chunkIndex, ferr)
			}
			if quicErr == nil {
				if werr := sendFrameViaQUIC(quicStream, frame); werr != nil {
					return fmt.Errorf("send shard %d chunk %d (QUIC): %w", shardIndex, chunkIndex, werr)
				}
			} else {
				udpPacer.wait()
				t0 := time.Now()
				werr := client.SendRawToPeer(peerID, frame)
				udpPacer.adjust(time.Since(t0), werr == nil)
				if werr != nil {
					return fmt.Errorf("send shard %d chunk %d: %w", shardIndex, chunkIndex, werr)
				}
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			transport := "UDP"
			if quicErr == nil {
				transport = "QUIC"
			}
			totalChunksSent := chunkIndex + 1
			fmt.Printf("[Transfer] Shard %d sent to %s (%d chunks, %s)\n", shardIndex, peerID[:8], totalChunksSent, transport)
			doneMsg := api.NewShardStreamDoneMessage(client.GetID(), api.ShardStreamDoneData{
				FileHash:    fileHash,
				ShardIndex:  shardIndex,
				TotalChunks: totalChunksSent,
			})
			client.SendToPeer(peerID, doneMsg) //nolint:errcheck — best-effort
			return nil
		}
		if err != nil {
			return fmt.Errorf("read shard %d: %w", shardIndex, err)
		}
	}
}
