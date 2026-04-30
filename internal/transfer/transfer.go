package transfer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/encoding"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

const (
	DataShards   = 10
	ParityShards = 4
	TotalShards  = DataShards + ParityShards

	// 32 KB chunks — safe under the 65507-byte UDP max even after AES-GCM (+28 bytes)
	// and binary header (+~57 bytes). 8× larger than the old 4 KB JSON chunks,
	// so 8× fewer packets for the same file.
	chunkSize = 32 * 1024

	// binaryMagic is the first byte of every binary shard frame.
	// JSON messages always start with '{' (0x7B) so 0x01 is unambiguous.
	binaryMagic byte = 0x01
)

// ShardMeta is stored alongside each shard set so FetchFileBytes can look up
// fileHash and fileSize from just a filename, without consulting the manifest.
type ShardMeta struct {
	FileName        string `json:"fileName"`
	FileHash        string `json:"fileHash"`
	FileSize        int    `json:"fileSize"`
	TotalDataShards int    `json:"totalDataShards"`
	TotalShards     int    `json:"totalShards"`
	BlockSize       int    `json:"blockSize"` // shard block size used during encoding
}

type shardAssembly struct {
	mu              sync.Mutex
	chunks          map[int][]byte
	totalChunks     int
	fileName        string
	fileHash        string
	fileSize        int
	shardIndex      int
	totalDataShards int
	totalShards     int
}

var (
	assemblyMu    sync.Mutex
	assemblies    = make(map[string]*shardAssembly) // key: "fileHash:shardIndex"
	reconstructed sync.Map                          // fileHash → true; prevents duplicate reconstruction

	sendLimiter = make(chan struct{}, 200)
	initOnce    sync.Once

	// shardStoredCb is called after a shard is successfully written to disk.
	// The daemon registers this to update the network manifest and broadcast.
	shardStoredCb   func(contentHash string, shardIndex int)
	shardStoredCbMu sync.Mutex

	// fileReadyChans allows FetchFileBytes to wait for autoReconstruct to finish.
	fileReadyChans sync.Map // fileHash → chan struct{}
)

// SetShardStoredCallback registers a function that is called (in a goroutine)
// each time a shard is fully assembled and written to disk. The daemon uses
// this to record shard ownership in the network manifest and broadcast.
func SetShardStoredCallback(fn func(contentHash string, shardIndex int)) {
	shardStoredCbMu.Lock()
	shardStoredCb = fn
	shardStoredCbMu.Unlock()
}

// testShardsDir is overridden in tests to redirect shard I/O to a temp dir.
var testShardsDir string

// ShardsDir returns ~/Mosaic/.shards — the base directory for all stored shards.
func ShardsDir() string {
	if testShardsDir != "" {
		return testShardsDir
	}
	return filepath.Join(shared.MosaicDir(), ".shards")
}

// ──────────────────────────────────────────────────────────
// Shard encryption key
// ──────────────────────────────────────────────────────────

// shardEncryptionKey loads the 32-byte AES-256 shard key cached at login time.
// The key is derived from the login key (HKDF-SHA256, info="mosaic-shard-key")
// and written to ~/.mosaic-shard.key during login so the raw login key is never
// stored on disk.
func shardEncryptionKey() ([32]byte, error) {
	var key [32]byte
	data, err := os.ReadFile(shared.ShardKeyPath())
	if err != nil {
		return key, fmt.Errorf("not logged in — run 'mos login <key>'")
	}
	if len(data) != 32 {
		return key, fmt.Errorf("shard key file is corrupt (expected 32 bytes, got %d)", len(data))
	}
	copy(key[:], data)
	return key, nil
}

// ──────────────────────────────────────────────────────────
// AES-256-GCM chunk encryption
// ──────────────────────────────────────────────────────────

func encryptChunk(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decryptChunk(key [32]byte, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// ──────────────────────────────────────────────────────────
// Binary shard frame encode / decode
//
// Frame layout (all integers little-endian):
//   [0]           magic byte (0x01)
//   [1..32]       fileHash  — 32 raw bytes (hex-decoded SHA-256)
//   [33]          filename length (uint8, max 255)
//   [34..33+fnL]  filename  — UTF-8 bytes
//   [+4]          fileSize  — uint32
//   [+1]          shardIndex — uint8
//   [+4]          chunkIndex — uint32
//   [+4]          totalChunks — uint32
//   [+1]          totalDataShards — uint8
//   [+1]          totalShards — uint8
//   [+4]          data length — uint32
//   [rest]        AES-GCM encrypted shard data
// ──────────────────────────────────────────────────────────

type binaryShardChunk struct {
	fileHash        string
	fileName        string
	fileSize        int
	shardIndex      int
	chunkIndex      int
	totalChunks     int
	totalDataShards int
	totalShards     int
	data            []byte
}

func encodeBinaryShardChunk(c binaryShardChunk) ([]byte, error) {
	hashBytes, err := hex.DecodeString(c.fileHash)
	if err != nil || len(hashBytes) != 32 {
		return nil, fmt.Errorf("invalid fileHash")
	}
	fnBytes := []byte(c.fileName)
	if len(fnBytes) > 255 {
		return nil, fmt.Errorf("filename too long")
	}

	hdrSize := 1 + 32 + 1 + len(fnBytes) + 4 + 1 + 4 + 4 + 1 + 1 + 4
	frame := make([]byte, hdrSize+len(c.data))
	off := 0

	frame[off] = binaryMagic
	off++
	copy(frame[off:], hashBytes)
	off += 32
	frame[off] = byte(len(fnBytes))
	off++
	copy(frame[off:], fnBytes)
	off += len(fnBytes)
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.fileSize))
	off += 4
	frame[off] = byte(c.shardIndex)
	off++
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.chunkIndex))
	off += 4
	binary.LittleEndian.PutUint32(frame[off:], uint32(c.totalChunks))
	off += 4
	frame[off] = byte(c.totalDataShards)
	off++
	frame[off] = byte(c.totalShards)
	off++
	binary.LittleEndian.PutUint32(frame[off:], uint32(len(c.data)))
	off += 4
	copy(frame[off:], c.data)

	return frame, nil
}

func decodeBinaryShardChunk(frame []byte) (*binaryShardChunk, error) {
	// minimum header without filename: 1+32+1+4+1+4+4+1+1+4 = 53 bytes
	if len(frame) < 53 {
		return nil, fmt.Errorf("frame too short (%d bytes)", len(frame))
	}
	if frame[0] != binaryMagic {
		return nil, fmt.Errorf("not a binary shard frame (magic=%02x)", frame[0])
	}

	off := 1
	fileHash := hex.EncodeToString(frame[off : off+32])
	off += 32

	fnLen := int(frame[off])
	off++
	if len(frame) < off+fnLen+4+1+4+4+1+1+4 {
		return nil, fmt.Errorf("frame too short for header")
	}
	fileName := string(frame[off : off+fnLen])
	off += fnLen

	fileSize := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	shardIndex := int(frame[off])
	off++
	chunkIndex := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	totalChunks := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4
	totalDataShards := int(frame[off])
	off++
	totalShards := int(frame[off])
	off++
	dataLen := int(binary.LittleEndian.Uint32(frame[off:]))
	off += 4

	if len(frame) < off+dataLen {
		return nil, fmt.Errorf("frame data truncated (need %d, have %d)", off+dataLen, len(frame))
	}

	return &binaryShardChunk{
		fileHash:        fileHash,
		fileName:        fileName,
		fileSize:        fileSize,
		shardIndex:      shardIndex,
		chunkIndex:      chunkIndex,
		totalChunks:     totalChunks,
		totalDataShards: totalDataShards,
		totalShards:     totalShards,
		data:            frame[off : off+dataLen],
	}, nil
}

// ──────────────────────────────────────────────────────────
// Rate limiter
// ──────────────────────────────────────────────────────────

// Init starts the send rate-limiter goroutine (20 K tokens/sec).
// Safe to call multiple times; only the first call takes effect.
func Init(ctx context.Context) {
	initOnce.Do(func() {
		for i := 0; i < 200; i++ {
			sendLimiter <- struct{}{}
		}
		go func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					for i := 0; i < 200; i++ {
						select {
						case sendLimiter <- struct{}{}:
						default:
						}
					}
				}
			}
		}()
	})
}

// ──────────────────────────────────────────────────────────
// Upload
// ──────────────────────────────────────────────────────────

// UploadFile RS-encodes path, saves shards locally, and streams them to all
// connected peers using the binary wire protocol. Wrap in a goroutine for background use.
func UploadFile(path string, client *p2p.Client) {
	filename := filepath.Base(path)
	nameNoExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("[Transfer] Cannot open %s: %v\n", path, err)
		return
	}
	hasher := sha256.New()
	fileSize64, err := io.Copy(hasher, f)
	f.Close()
	if err != nil {
		fmt.Printf("[Transfer] Cannot hash %s: %v\n", path, err)
		return
	}
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	fileSize := int(fileSize64)

	netKey, err := shardEncryptionKey()
	if err != nil {
		fmt.Printf("[Transfer] Cannot derive shard key: %v\n", err)
		return
	}

	fmt.Printf("[Transfer] Uploading %s  hash=%s…  size=%d bytes\n", filename, fileHash[:12], fileSize)

	outDir, err := os.MkdirTemp("", "mosaic-upload-*")
	if err != nil {
		fmt.Printf("[Transfer] Cannot create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(outDir)

	if err := copyFile(path, filepath.Join(outDir, filename)); err != nil {
		fmt.Printf("[Transfer] Cannot stage file: %v\n", err)
		return
	}

	fmt.Printf("[Transfer] Encoding into %d data + %d parity shards…\n", DataShards, ParityShards)
	enc, err := encoding.NewEncoder(DataShards, ParityShards, outDir, outDir)
	if err != nil {
		fmt.Printf("[Transfer] Encoder init failed: %v\n", err)
		return
	}
	if err := enc.EncodeFile(filename); err != nil {
		fmt.Printf("[Transfer] Encode failed: %v\n", err)
		return
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

	var wg sync.WaitGroup
	sem := make(chan struct{}, 1) // sequential sends to avoid UDP packet loss

	for i := 0; i < TotalShards; i++ {
		srcPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", i, nameNoExt))
		targetIndex := i % numNodes

		if targetIndex == ourIndex {
			// Our shard: encrypt and persist locally, register in ShardMap.
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			if chunks, err := encryptShardFileToChunks(srcPath, netKey); err == nil {
				if writeErr := writeEncryptedShardFile(dst, chunks); writeErr == nil {
					shardStoredCbMu.Lock()
					cb := shardStoredCb
					shardStoredCbMu.Unlock()
					if cb != nil {
						go cb(fileHash, i)
					}
				}
			}
		} else {
			// Peer's shard: send directly without storing locally.
			targetPeerID := ids[targetIndex]
			wg.Add(1)
			sem <- struct{}{}
			go func(shardIdx int, peerID string, src string) {
				defer func() { <-sem }()
				defer wg.Done()
				if err := sendPlaintextShardToPeer(src, shardIdx, fileHash, filename, fileSize, netKey, peerID, client); err != nil {
					fmt.Printf("[Transfer] Shard %d → peer %s failed: %v\n", shardIdx, peerID[:8], err)
				}
			}(i, targetPeerID, srcPath)
		}
	}
	wg.Wait()

	writeShardMeta(shardDir, ShardMeta{
		FileName:        filename,
		FileHash:        fileHash,
		FileSize:        fileSize,
		TotalDataShards: DataShards,
		TotalShards:     TotalShards,
		BlockSize:       enc.BlockSize(),
	})

	if len(connectedPeers) == 0 {
		fmt.Println("[Transfer] No peers connected — all shards saved locally")
		return
	}
	fmt.Printf("[Transfer] Upload complete: %s\n", filename)
}

// ──────────────────────────────────────────────────────────
// Receive
// ──────────────────────────────────────────────────────────

// HandleBinaryShardChunk processes a raw binary shard frame received from a peer.
// Called directly from the message router when data[0] == binaryMagic.
// The chunk data is stored encrypted — peers never decrypt; only the file owner
// decrypts at reconstruction time (Option A blind-courier model).
func HandleBinaryShardChunk(data []byte) {
	c, err := decodeBinaryShardChunk(data)
	if err != nil {
		fmt.Printf("[Transfer] Bad binary frame: %v\n", err)
		return
	}

	key := fmt.Sprintf("%s:%d", c.fileHash, c.shardIndex)

	assemblyMu.Lock()
	asm, ok := assemblies[key]
	if !ok {
		asm = &shardAssembly{
			chunks:          make(map[int][]byte),
			totalChunks:     c.totalChunks,
			fileName:        c.fileName,
			fileHash:        c.fileHash,
			fileSize:        c.fileSize,
			shardIndex:      c.shardIndex,
			totalDataShards: c.totalDataShards,
			totalShards:     c.totalShards,
		}
		assemblies[key] = asm
	}
	assemblyMu.Unlock()

	asm.mu.Lock()
	asm.chunks[c.chunkIndex] = c.data // store encrypted blob as-is
	received := len(asm.chunks)
	total := asm.totalChunks
	asm.mu.Unlock()

	if received%100 == 0 || received == total {
		fmt.Printf("[Recv] Shard %d: %d/%d chunks\n", c.shardIndex, received, total)
	}

	if received == total {
		assemblyMu.Lock()
		finalAsm := assemblies[key]
		delete(assemblies, key)
		assemblyMu.Unlock()
		go finalizeShard(finalAsm)
	}
}

func finalizeShard(asm *shardAssembly) {
	shardDir := filepath.Join(ShardsDir(), asm.fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		fmt.Printf("[Transfer] Cannot create shard dir: %v\n", err)
		return
	}

	// Collect chunks in order (they arrive encrypted; stored as-is).
	orderedChunks := make([][]byte, asm.totalChunks)
	for i := 0; i < asm.totalChunks; i++ {
		chunk, ok := asm.chunks[i]
		if !ok {
			fmt.Printf("[Transfer] Missing chunk %d for shard %d\n", i, asm.shardIndex)
			return
		}
		orderedChunks[i] = chunk
	}

	shardPath := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", asm.shardIndex, asm.fileHash))
	if err := writeEncryptedShardFile(shardPath, orderedChunks); err != nil {
		fmt.Printf("[Transfer] Cannot write shard %d: %v\n", asm.shardIndex, err)
		return
	}
	fmt.Printf("[Transfer] Shard %d assembled → %s\n", asm.shardIndex, shardPath)

	// Notify the daemon that this node now holds this shard.
	shardStoredCbMu.Lock()
	cb := shardStoredCb
	shardStoredCbMu.Unlock()
	if cb != nil {
		go cb(asm.fileHash, asm.shardIndex)
	}

	writeShardMeta(shardDir, ShardMeta{
		FileName:        asm.fileName,
		FileHash:        asm.fileHash,
		FileSize:        asm.fileSize,
		TotalDataShards: asm.totalDataShards,
		TotalShards:     asm.totalShards,
		BlockSize:       encoding.ComputeBlockSize(asm.fileSize, asm.totalDataShards),
	})

	count := 0
	for i := 0; i < asm.totalDataShards; i++ {
		p := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, asm.fileHash))
		if _, err := os.Stat(p); err == nil {
			count++
		}
	}
	if count >= asm.totalDataShards {
		if _, already := reconstructed.LoadOrStore(asm.fileHash, true); !already {
			go autoReconstruct(asm)
		}
	}
}

func autoReconstruct(asm *shardAssembly) {
	mosaicDir := shared.MosaicDir()
	outDir, err := os.MkdirTemp("", "mosaic-recon-*")
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: cannot create output dir: %v\n", err)
		return
	}
	defer os.RemoveAll(outDir)

	key, err := shardEncryptionKey()
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: cannot derive shard key: %v\n", err)
		reconstructed.Delete(asm.fileHash) // allow retry when key is available
		return
	}

	// Decrypt encrypted shard blobs into a temp plaintext dir for the RS decoder.
	plainDir, err := os.MkdirTemp("", "mosaic-plain-*")
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: cannot create plaintext dir: %v\n", err)
		reconstructed.Delete(asm.fileHash)
		return
	}
	defer os.RemoveAll(plainDir)
	decrypted, err := decryptShardsToDir(asm.fileHash, asm.totalShards, key, plainDir)
	if err != nil || decrypted == 0 {
		// Zero decrypted shards means wrong key — this node is not the file owner.
		reconstructed.Delete(asm.fileHash)
		return
	}

	enc, err := encoding.NewEncoder(asm.totalDataShards, asm.totalShards-asm.totalDataShards, outDir, plainDir)
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: encoder init failed: %v\n", err)
		return
	}
	// Use stored block size if available; fall back to computing from file size.
	blockSize := encoding.ComputeBlockSize(asm.fileSize, asm.totalDataShards)
	if m := FindShardMetaByHash(asm.fileHash); m != nil && m.BlockSize > 0 {
		blockSize = m.BlockSize
	}
	enc.SetBlockSize(blockSize)
	fmt.Printf("[Transfer] Reconstructing %s…\n", asm.fileHash[:12])
	if err := enc.DecodeShards(asm.fileHash, asm.fileSize); err != nil {
		fmt.Printf("[Transfer] Reconstruct: decode failed: %v\n", err)
		return
	}

	matches, _ := filepath.Glob(filepath.Join(outDir, asm.fileHash+"*"))
	if len(matches) == 0 {
		fmt.Printf("[Transfer] Reconstruct: output file not found\n")
		return
	}

	destPath := filepath.Join(mosaicDir, asm.fileName)
	if err := copyFile(matches[0], destPath); err != nil {
		fmt.Printf("[Transfer] Reconstruct: could not write %s: %v\n", destPath, err)
		return
	}
	fmt.Printf("[Transfer] File ready: %s\n", destPath)

	// Unblock any FetchFileBytes call that is waiting for this file.
	if v, ok := fileReadyChans.Load(asm.fileHash); ok {
		ch := v.(chan struct{})
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ──────────────────────────────────────────────────────────
// Download
// ──────────────────────────────────────────────────────────

// EnsureShardMeta writes a meta.json for the given file if one does not already
// exist. Call this when you have file info from the network manifest but no
// shards have been received yet, so that FetchFileBytes can proceed to request
// missing shards from peers rather than bailing out immediately.
func EnsureShardMeta(fileHash, fileName string, fileSize int) {
	if FindShardMetaByHash(fileHash) != nil {
		return
	}
	shardDir := filepath.Join(ShardsDir(), fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		return
	}
	writeShardMeta(shardDir, ShardMeta{
		FileName:        fileName,
		FileHash:        fileHash,
		FileSize:        fileSize,
		TotalDataShards: DataShards,
		TotalShards:     TotalShards,
		BlockSize:       encoding.ComputeBlockSize(fileSize, DataShards),
	})
}

// FindShardMeta scans the local shard directory for a file matching filename
// and returns its ShardMeta, or nil if not found.
func FindShardMeta(filename string) *ShardMeta {
	entries, err := os.ReadDir(ShardsDir())
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ShardsDir(), e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var m ShardMeta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.FileName == filename {
			return &m
		}
	}
	return nil
}

// FindShardMetaByHash returns the ShardMeta for a given content hash, or nil.
func FindShardMetaByHash(contentHash string) *ShardMeta {
	data, err := os.ReadFile(filepath.Join(ShardsDir(), contentHash, "meta.json"))
	if err != nil {
		return nil
	}
	var m ShardMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &m
}

// missingDataShards returns the shard indices (0..totalDataShards-1) that are
// not yet present on disk for the given file hash.
func missingDataShards(fileHash string, totalDataShards int) []int {
	shardDir := filepath.Join(ShardsDir(), fileHash)
	var missing []int
	for i := 0; i < totalDataShards; i++ {
		p := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, i)
		}
	}
	return missing
}

// StoreShardData writes raw shard bytes (received via ShardResponse) to the
// local shard directory and triggers reconstruction if enough data shards are
// now present. fileName, fileSize, totalDataShards, and totalShards must come
// from the network manifest entry for this file.
func StoreShardData(fileHash, fileName string, fileSize, shardIndex, totalDataShards, totalShards int, data []byte) {
	shardDir := filepath.Join(ShardsDir(), fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		fmt.Printf("[Transfer] StoreShardData: cannot create shard dir: %v\n", err)
		return
	}

	shardPath := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", shardIndex, fileHash))
	if err := os.WriteFile(shardPath, data, 0644); err != nil {
		fmt.Printf("[Transfer] StoreShardData: cannot write shard %d: %v\n", shardIndex, err)
		return
	}
	fmt.Printf("[Transfer] Stored received shard %d for %s\n", shardIndex, fileHash[:12])

	// Preserve existing block size if we already have meta; otherwise compute it.
	bs := encoding.ComputeBlockSize(fileSize, totalDataShards)
	if existing := FindShardMetaByHash(fileHash); existing != nil && existing.BlockSize > 0 {
		bs = existing.BlockSize
	}
	writeShardMeta(shardDir, ShardMeta{
		FileName:        fileName,
		FileHash:        fileHash,
		FileSize:        fileSize,
		TotalDataShards: totalDataShards,
		TotalShards:     totalShards,
		BlockSize:       bs,
	})

	// Notify manifest that we now hold this shard.
	shardStoredCbMu.Lock()
	cb := shardStoredCb
	shardStoredCbMu.Unlock()
	if cb != nil {
		go cb(fileHash, shardIndex)
	}

	// Trigger reconstruction if we now have enough data shards.
	count := totalDataShards - len(missingDataShards(fileHash, totalDataShards))
	if count >= totalDataShards {
		asm := &shardAssembly{
			fileName:        fileName,
			fileHash:        fileHash,
			fileSize:        fileSize,
			totalDataShards: totalDataShards,
			totalShards:     totalShards,
		}
		if _, already := reconstructed.LoadOrStore(fileHash, true); !already {
			go autoReconstruct(asm)
		}
	}
}

// FetchFileBytes reconstructs a file from locally stored shards. If shards
// are missing and a P2P client + shard-holder lookup are provided, it requests
// the missing shards from peers and waits up to 60 s for reconstruction.
func FetchFileBytes(filename string, client *p2p.Client, getHolders func(contentHash string, shardIndex int) []string) ([]byte, error) {
	meta := FindShardMeta(filename)
	if meta == nil {
		return nil, fmt.Errorf("shards for %q not found — file may not have been received yet", filename)
	}

	missing := missingDataShards(meta.FileHash, meta.TotalDataShards)

	if len(missing) > 0 && client != nil && getHolders != nil {
		sign := api.NewSignature(client.GetID())
		for _, idx := range missing {
			holders := getHolders(meta.FileHash, idx)
			if len(holders) == 0 {
				fmt.Printf("[Transfer] No known holders for shard %d of %s\n", idx, filename)
				continue
			}
			fmt.Printf("[Transfer] Requesting shard %d of %s from %d peer(s)\n", idx, filename, len(holders))
			msg := api.NewShardRequestMessage(sign, api.ShardRequestData{
				FileHash:   meta.FileHash,
				ShardIndex: idx,
			})
			_ = client.SendToAllPeers(msg)
		}

		// Wait for autoReconstruct to signal that the file is ready.
		ch := make(chan struct{}, 1)
		fileReadyChans.Store(meta.FileHash, ch)
		defer fileReadyChans.Delete(meta.FileHash)

		select {
		case <-ch:
			// File written to ~/Mosaic/ by autoReconstruct — read it back.
			destPath := filepath.Join(shared.MosaicDir(), filename)
			return os.ReadFile(destPath)
		case <-time.After(60 * time.Second):
			return nil, fmt.Errorf("timed out waiting for shards of %q from peers", filename)
		}
	}

	// All shards present locally — decrypt to a temp dir then reconstruct.
	key, err := shardEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("cannot derive shard key: %w", err)
	}

	plainDir, err := os.MkdirTemp("", "mosaic-plain-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(plainDir)
	if _, err := decryptShardsToDir(meta.FileHash, meta.TotalShards, key, plainDir); err != nil {
		return nil, fmt.Errorf("decrypt shards: %w", err)
	}

	outDir, err := os.MkdirTemp("", "mosaic-fetch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	enc, err := encoding.NewEncoder(meta.TotalDataShards, meta.TotalShards-meta.TotalDataShards, outDir, plainDir)
	if err != nil {
		return nil, fmt.Errorf("encoder init: %w", err)
	}
	// Use stored block size if available; fall back to computing from file size.
	blockSize := encoding.ComputeBlockSize(meta.FileSize, meta.TotalDataShards)
	if meta.BlockSize > 0 {
		blockSize = meta.BlockSize
	}
	enc.SetBlockSize(blockSize)
	if err := enc.DecodeShards(meta.FileHash, meta.FileSize); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	matches, _ := filepath.Glob(filepath.Join(outDir, meta.FileHash+"*"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("reconstructed file not found in %s", outDir)
	}
	return os.ReadFile(matches[0])
}

// ──────────────────────────────────────────────────────────
// Shard redistribution
// ──────────────────────────────────────────────────────────

// sendPlaintextShardToPeer reads a plaintext shard file from the RS encoder's
// temp directory, encrypts each chunk, and sends it as binary frames to one peer.
// Used during upload so peer-bound shards are never written to the uploader's disk.
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
			<-sendLimiter
			if werr := client.SendRawToPeer(peerID, frame); werr != nil {
				return fmt.Errorf("send shard %d chunk %d: %w", shardIndex, chunkIndex, werr)
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			fmt.Printf("[Transfer] Shard %d sent to %s (%d chunks)\n", shardIndex, peerID[:8], chunkIndex+1)
			return nil
		}
		if err != nil {
			return fmt.Errorf("read shard %d: %w", shardIndex, err)
		}
	}
}

// StreamShardToPeer reads a locally stored encrypted shard and forwards its
// chunks to a specific peer using the binary wire protocol. The chunks are
// sent as-is — no decryption or re-encryption needed (blind-courier model).
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

		<-sendLimiter
		if err := client.SendRawToPeer(peerID, frame); err != nil {
			fmt.Printf("[Transfer] StreamShardToPeer: shard %d chunk %d → %s failed: %v\n", shardIndex, chunkIdx, peerID[:8], err)
			return
		}
	}
	fmt.Printf("[Transfer] Redistributed shard %d of %s → peer %s\n", shardIndex, fileHash[:12], peerID[:8])
}

// ──────────────────────────────────────────────────────────
// Shard request / response (still JSON — low-frequency control messages)
// ──────────────────────────────────────────────────────────

// HandleShardRequest responds to a peer requesting a shard we have stored.
func HandleShardRequest(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetShardRequestData()
	if err != nil {
		return
	}
	pattern := filepath.Join(ShardsDir(), d.FileHash, fmt.Sprintf("shard%d_*.dat", d.ShardIndex))
	matches, _ := filepath.Glob(pattern)
	sign := api.NewSignature(client.GetID())

	if len(matches) == 0 {
		_ = client.SendToAllPeers(api.NewShardResponseMessage(sign, api.ShardResponseData{
			FileHash: d.FileHash, ShardIndex: d.ShardIndex, Found: false,
		}))
		return
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return
	}
	_ = client.SendToAllPeers(api.NewShardResponseMessage(sign, api.ShardResponseData{
		FileHash: d.FileHash, ShardIndex: d.ShardIndex, Found: true, Data: data,
	}))
}

// ──────────────────────────────────────────────────────────
// Encrypted shard file I/O
//
// On-disk format (all integers little-endian):
//   [4 bytes] totalChunks
//   for each chunk:
//     [4 bytes] chunk length
//     [N bytes] AES-GCM encrypted chunk data
// ──────────────────────────────────────────────────────────

// writeEncryptedShardFile writes pre-encrypted chunks to a shard file.
func writeEncryptedShardFile(path string, chunks [][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(chunks)))
	if _, err := f.Write(hdr[:]); err != nil {
		return err
	}
	for _, chunk := range chunks {
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(chunk)))
		if _, err := f.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := f.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

// decryptShardToPlaintext reads a length-prefixed encrypted shard file and
// returns the concatenated plaintext.
func decryptShardToPlaintext(path string, key [32]byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, fmt.Errorf("read totalChunks: %w", err)
	}
	totalChunks := int(binary.LittleEndian.Uint32(hdr[:]))

	var plain []byte
	for i := 0; i < totalChunks; i++ {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			return nil, fmt.Errorf("read chunk %d len: %w", i, err)
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		encrypted := make([]byte, n)
		if _, err := io.ReadFull(f, encrypted); err != nil {
			return nil, fmt.Errorf("read chunk %d data: %w", i, err)
		}
		dec, err := decryptChunk(key, encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt chunk %d: %w", i, err)
		}
		plain = append(plain, dec...)
	}
	return plain, nil
}

// encryptShardFileToChunks reads a plaintext shard file and returns AES-GCM
// encrypted slices, one per chunkSize window.
func encryptShardFileToChunks(path string, key [32]byte) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var chunks [][]byte
	buf := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			enc, eerr := encryptChunk(key, buf[:n])
			if eerr != nil {
				return nil, eerr
			}
			chunks = append(chunks, enc)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return chunks, nil
}

// decryptShardsToDir decrypts all locally stored shards for fileHash into
// destDir/fileHash/ as flat plaintext files ready for the RS decoder.
// Missing shards are skipped — RS handles them as erasures.
// Returns the number of shards successfully decrypted.
func decryptShardsToDir(fileHash string, totalShards int, key [32]byte, destDir string) (int, error) {
	shardDir := filepath.Join(ShardsDir(), fileHash)
	outDir := filepath.Join(destDir, fileHash)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return 0, err
	}
	decrypted := 0
	for i := 0; i < totalShards; i++ {
		src := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if _, err := os.Stat(src); err != nil {
			continue
		}
		plain, err := decryptShardToPlaintext(src, key)
		if err != nil {
			continue // wrong key or corrupt — skip silently
		}
		dst := filepath.Join(outDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if err := os.WriteFile(dst, plain, 0644); err != nil {
			return decrypted, err
		}
		decrypted++
	}
	return decrypted, nil
}

// ──────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────

func writeShardMeta(shardDir string, m ShardMeta) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(shardDir, "meta.json"), data, 0644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
