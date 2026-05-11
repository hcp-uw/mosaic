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
	"sync/atomic"
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

	// 8 KB chunks — each frame (~8,133 bytes total after AES-GCM and binary header)
	// fragments into ~6 IPv4 packets at 1500-byte MTU, vs ~42 for 60 KB chunks.
	// Fewer fragments per datagram means far fewer chunk losses on lossy paths
	// (university WiFi, TURN relay) where a single dropped fragment kills the chunk.
	chunkSize = 8 * 1024

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

	udpPacer = &adaptivePacer{interval: pacerInitInterval}
	initOnce sync.Once

	// shardStoredCb is called after a shard is successfully written to disk.
	// The daemon registers this to update the network manifest and broadcast.
	shardStoredCb   func(contentHash string, shardIndex int)
	shardStoredCbMu sync.Mutex

	// shardSentCb is called after a shard is successfully sent to a peer.
	// The daemon registers this to record the peer as a shard holder immediately,
	// without waiting for the receiver to assemble and report back.
	shardSentCb   func(contentHash string, shardIndex int, peerID string)
	shardSentCbMu sync.Mutex

	// fileReadyChans allows FetchFileBytes to wait for autoReconstruct to finish.
	fileReadyChans sync.Map // fileHash → chan struct{}

	// shardReadyChans allows FetchFileBytes to wait for a specific shard.
	// key: "fileHash:shardIndex" → chan struct{}
	shardReadyChans sync.Map

	// shardRelayCallback is called when a shard arrives that was originally
	// requested by a different peer (relay scenario). The daemon registers this
	// so it can forward the shard to the original requester via binary streaming.
	shardRelayCallback   func(fileHash string, shardIndex int, requesterIDs []string)
	shardRelayCallbackMu sync.Mutex

	// pendingShardRequests tracks shards we are relaying on behalf of other peers.
	// Key: "fileHash:shardIndex" → list of requester P2P IDs awaiting that shard.
	pendingShardRequestsMu sync.Mutex
	pendingShardRequests   = make(map[string][]string)

	// uploadProgress tracks shard distribution progress for the current upload.
	// shardsDispatched is incremented each time a shard is stored or sent.
	// shardsTotal is set at the start of UploadFile.
	uploadShardsDispatched atomic.Int32
	uploadShardsTotal      atomic.Int32

	// joinSync tracks shards received from peers after a network join.
	// Used by the CLI to block until redistribution settles.
	joinShardsReceived        atomic.Int32
	joinLastShardActivityNano atomic.Int64 // Unix nanoseconds; 0 = no activity yet

	// downloadProgress tracks shard fetching during a FetchFileBytes call.
	downloadShardsNeeded   atomic.Int32
	downloadShardsReceived atomic.Int32
	downloadTargetHash     string
	downloadTargetMu       sync.Mutex

	// lastChunkReceivedNano is updated on every incoming shard chunk so that
	// FetchFileBytes can use an idle timeout instead of a fixed deadline.
	lastChunkReceivedNano atomic.Int64

	// shardLastChunkNano tracks the last chunk arrival time per shard so the
	// per-shard idle timer in FetchFileBytes resets only when chunks for THAT
	// specific shard arrive, not when unrelated shards of other files arrive.
	// key: "fileHash:shardIndex" → Unix nanoseconds (int64)
	shardLastChunkNano sync.Map
)

// SetShardRelayCallback registers a callback invoked when a shard arrives that
// had pending relay requesters. The daemon uses this to forward the shard to
// the original requester(s) via binary streaming.
func SetShardRelayCallback(fn func(fileHash string, shardIndex int, requesterIDs []string)) {
	shardRelayCallbackMu.Lock()
	shardRelayCallback = fn
	shardRelayCallbackMu.Unlock()
}

func registerPendingShardRequest(fileHash string, shardIndex int, requesterID string) {
	key := fmt.Sprintf("%s:%d", fileHash, shardIndex)
	pendingShardRequestsMu.Lock()
	pendingShardRequests[key] = append(pendingShardRequests[key], requesterID)
	pendingShardRequestsMu.Unlock()
}

func takePendingShardRequesters(fileHash string, shardIndex int) []string {
	key := fmt.Sprintf("%s:%d", fileHash, shardIndex)
	pendingShardRequestsMu.Lock()
	ids := pendingShardRequests[key]
	delete(pendingShardRequests, key)
	pendingShardRequestsMu.Unlock()
	return ids
}

// SetShardStoredCallback registers a function that is called (in a goroutine)
// each time a shard is fully assembled and written to disk. The daemon uses
// this to record shard ownership in the network manifest and broadcast.
func SetShardStoredCallback(fn func(contentHash string, shardIndex int)) {
	shardStoredCbMu.Lock()
	shardStoredCb = fn
	shardStoredCbMu.Unlock()
}

// SetShardSentCallback registers a function that is called after a shard is
// successfully sent to a peer. The daemon uses this to record the peer as a
// holder immediately, without waiting for the peer's assembly to complete.
func SetShardSentCallback(fn func(contentHash string, shardIndex int, peerID string)) {
	shardSentCbMu.Lock()
	shardSentCb = fn
	shardSentCbMu.Unlock()
}

// GetUploadProgress returns the number of shards dispatched so far and the
// total shards for the current upload. Both are 0 when no upload is running.
func GetUploadProgress() (dispatched, total int) {
	return int(uploadShardsDispatched.Load()), int(uploadShardsTotal.Load())
}

// resetUploadProgress initialises the counters for a new upload.
func resetUploadProgress(total int) {
	uploadShardsDispatched.Store(0)
	uploadShardsTotal.Store(int32(total))
}

// ResetJoinSync clears the join-sync counters at the start of a new join.
func ResetJoinSync() {
	joinShardsReceived.Store(0)
	joinLastShardActivityNano.Store(0)
}

// GetJoinShardActivity returns how many shards have been received from peers
// since the last join, and the Unix-nanosecond timestamp of the last one.
func GetJoinShardActivity() (received int, lastNano int64) {
	return int(joinShardsReceived.Load()), joinLastShardActivityNano.Load()
}

// SetDownloadTarget arms the download-progress counters for a FetchFileBytes call.
func SetDownloadTarget(fileHash string, needed int) {
	downloadTargetMu.Lock()
	downloadTargetHash = fileHash
	downloadTargetMu.Unlock()
	downloadShardsNeeded.Store(int32(needed))
	downloadShardsReceived.Store(0)
}

// ClearDownloadTarget disarms the download-progress counters after a fetch.
func ClearDownloadTarget() {
	downloadTargetMu.Lock()
	downloadTargetHash = ""
	downloadTargetMu.Unlock()
	downloadShardsNeeded.Store(0)
	downloadShardsReceived.Store(0)
}

// GetDownloadProgress returns (received, needed) for the active FetchFileBytes call.
func GetDownloadProgress() (received, needed int) {
	return int(downloadShardsReceived.Load()), int(downloadShardsNeeded.Load())
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
// Adaptive UDP pacer
// ──────────────────────────────────────────────────────────

// adaptivePacer paces UDP sends using AIMD (additive-increase /
// multiplicative-decrease), with kernel WriteTo latency as the congestion
// signal. A fast WriteTo means the OS send buffer has headroom → speed up.
// A slow/blocking WriteTo means the buffer is backing up → slow down.
// A hard send error triggers an immediate rate halving.
//
// Bounds: 100 µs–100 ms between sends (10,000/sec down to 10/sec).
// Start:  2 ms (500/sec × 8 KB ≈ 32 Mbps) — conservative for TURN/WiFi.
type adaptivePacer struct {
	mu       sync.Mutex
	interval time.Duration
	nextSend time.Time
}

const (
	pacerInitInterval = 2 * time.Millisecond   // 500 sends/sec start
	pacerMinInterval  = 100 * time.Microsecond // 10,000 sends/sec ceiling
	pacerMaxInterval  = 100 * time.Millisecond // 10 sends/sec floor
)

// wait blocks until the next send slot is available, then reserves it.
// Resets if idle to prevent a burst after a quiet period.
func (p *adaptivePacer) wait() {
	p.mu.Lock()
	now := time.Now()
	if p.nextSend.Before(now) {
		p.nextSend = now // idle reset — no burst catch-up
	}
	sleep := p.nextSend.Sub(now)
	p.nextSend = p.nextSend.Add(p.interval)
	p.mu.Unlock()
	if sleep > 0 {
		time.Sleep(sleep)
	}
}

// adjust tunes the interval after each send.
//   - Hard error            → double the interval (halve throughput)
//   - WriteTo slow (> 5 ms) → +25 % interval (kernel buffer backing up)
//   - WriteTo fast (< 500 µs) → −5 % interval (room to go faster)
func (p *adaptivePacer) adjust(writeLatency time.Duration, sendOK bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.interval
	switch {
	case !sendOK:
		p.interval = min(cur*2, pacerMaxInterval)
	case writeLatency > 5*time.Millisecond:
		p.interval = min(time.Duration(float64(cur)*1.25), pacerMaxInterval)
	case writeLatency < 500*time.Microsecond:
		p.interval = max(time.Duration(float64(cur)*0.95), pacerMinInterval)
	}
}

// Init resets the adaptive pacer to its initial rate. Safe to call multiple
// times; only the first call takes effect.
func Init(_ context.Context) {
	initOnce.Do(func() {
		udpPacer.mu.Lock()
		udpPacer.interval = pacerInitInterval
		udpPacer.nextSend = time.Time{}
		udpPacer.mu.Unlock()
	})
}

// ──────────────────────────────────────────────────────────
// Upload
// ──────────────────────────────────────────────────────────

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
			// Peer's shard: send directly without storing locally.
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
		return fileHash, fileSize, nil
	}
	fmt.Printf("[Transfer] Upload complete: %s\n", filename)
	return fileHash, fileSize, nil
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

	now := time.Now().UnixNano()
	lastChunkReceivedNano.Store(now)
	shardLastChunkNano.Store(key, now)

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

	// Unblock any FetchFileBytes call that was waiting for this specific shard.
	// Must happen immediately after the shard lands on disk — not inside
	// autoReconstruct — so the sequential per-shard wait loop doesn't have to
	// wait for all data shards to accumulate before it can advance.
	shardKey := fmt.Sprintf("%s:%d", asm.fileHash, asm.shardIndex)
	if v, ok := shardReadyChans.LoadAndDelete(shardKey); ok {
		ch := v.(chan struct{})
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	// Track join-sync progress: every assembled shard came from a peer.
	joinShardsReceived.Add(1)
	joinLastShardActivityNano.Store(time.Now().UnixNano())

	// Notify the daemon that this node now holds this shard.
	shardStoredCbMu.Lock()
	cb := shardStoredCb
	shardStoredCbMu.Unlock()
	if cb != nil {
		go cb(asm.fileHash, asm.shardIndex)
	}

	// If any peers were waiting for this shard via relay, notify them now.
	if requesters := takePendingShardRequesters(asm.fileHash, asm.shardIndex); len(requesters) > 0 {
		shardRelayCallbackMu.Lock()
		relayCb := shardRelayCallback
		shardRelayCallbackMu.Unlock()
		if relayCb != nil {
			go relayCb(asm.fileHash, asm.shardIndex, requesters)
		}
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

	// Track download progress when this shard was fetched for FetchFileBytes.
	downloadTargetMu.Lock()
	if downloadTargetHash == fileHash {
		downloadShardsReceived.Add(1)
	}
	downloadTargetMu.Unlock()

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
		if !client.IsPeerCommunicationAvailable() {
			return nil, fmt.Errorf("no peers connected to request shards for %q", filename)
		}

		sign := api.NewSignature(client.GetID())

		// Arm download-progress counters so the CLI can show a progress bar.
		SetDownloadTarget(meta.FileHash, len(missing))
		defer ClearDownloadTarget()

		// Register file-ready channel before requesting any shards so we
		// don't miss a signal that fires while we're still in the loop.
		fileCh := make(chan struct{}, 1)
		fileReadyChans.Store(meta.FileHash, fileCh)
		defer fileReadyChans.Delete(meta.FileHash)

		// Request missing shards one at a time, mirroring the upload path's
		// sequential-per-peer delivery (semaphore = 1 for 1 peer). Requesting
		// all shards simultaneously causes the holder to launch N concurrent
		// StreamShardToPeer goroutines that fight each other for bandwidth.
		const (
			// How long to wait before the first chunk of a shard arrives.
			// A lost request or unreachable peer shows up quickly — 10 s is
			// plenty for any connection faster than ~8 Kbps.
			shardFirstChunkTimeout = 10 * time.Second
			// How long to wait between consecutive chunks once a shard is
			// actively streaming. Resets on every incoming chunk of that shard.
			shardIdleTimeout = 30 * time.Second
			shardMaxRetries  = 2
		)

		for _, idx := range missing {
			// The shard may have arrived from join redistribution while we were
			// waiting for earlier shards — skip if it's already on disk.
			shardPath := filepath.Join(ShardsDir(), meta.FileHash,
				fmt.Sprintf("shard%d_%s.dat", idx, meta.FileHash))
			if _, err := os.Stat(shardPath); err == nil {
				continue
			}

			holders := getHolders(meta.FileHash, idx)
			if len(holders) == 0 {
				fmt.Printf("[Transfer] No known holders for shard %d of %s — broadcasting to all peers\n", idx, filename)
			} else {
				fmt.Printf("[Transfer] Requesting shard %d of %s from %d known holder(s)\n", idx, filename, len(holders))
			}

			received := false
			for attempt := 0; attempt <= shardMaxRetries && !received; attempt++ {
				// Shard may have arrived via join redistribution or a concurrent
				// assembly between attempts — skip re-requesting if it's on disk.
				if _, err := os.Stat(shardPath); err == nil {
					received = true
					break
				}
				if attempt > 0 {
					fmt.Printf("[Transfer] Shard %d of %s — retry %d/%d\n", idx, filename, attempt, shardMaxRetries)
				}

				shardKey := fmt.Sprintf("%s:%d", meta.FileHash, idx)
				shardCh := make(chan struct{}, 1)
				shardReadyChans.Store(shardKey, shardCh)
				// Clear any stale per-shard activity stamp before sending the
				// request so we only see activity from this attempt onward.
				shardLastChunkNano.Delete(shardKey)

				msg := api.NewShardRequestMessage(sign, api.ShardRequestData{
					FileHash:   meta.FileHash,
					ShardIndex: idx,
				})
				_ = client.SendToAllPeers(msg)

				// Start with the short first-chunk timeout. Once any chunk of
				// this shard arrives the timer is upgraded to shardIdleTimeout
				// so we don't cut off a large shard that is actively streaming.
				idleTimer := time.NewTimer(shardFirstChunkTimeout)
				activityTick := time.NewTicker(5 * time.Second)
				var lastShardNano int64

			waitShard:
				for {
					select {
					case <-shardCh:
						received = true
						break waitShard
					case <-activityTick.C:
						// Reset idle timer only when chunks for THIS specific shard
						// arrive — not when unrelated shards of other files do.
						if v, ok := shardLastChunkNano.Load(shardKey); ok {
							if nano := v.(int64); nano != lastShardNano {
								lastShardNano = nano
								// First or subsequent chunk arrived — switch to the
								// longer idle timeout so large shards aren't cut off.
								idleTimer.Reset(shardIdleTimeout)
							}
						}
					case <-idleTimer.C:
						shardReadyChans.Delete(shardKey)
						break waitShard
					}
				}
				idleTimer.Stop()
				activityTick.Stop()
			}

			if !received {
				return nil, fmt.Errorf("shard %d of %q timed out after %d retries", idx, filename, shardMaxRetries)
			}
		}

		// All data shards are on disk — wait for autoReconstruct to write the file.
		// Check first in case reconstruction already completed during the loop.
		destPath := filepath.Join(shared.MosaicDir(), filename)
		if _, err := os.Stat(destPath); err == nil {
			return os.ReadFile(destPath)
		}
		select {
		case <-fileCh:
			return os.ReadFile(destPath)
		case <-time.After(30 * time.Second):
			return nil, fmt.Errorf("reconstruction of %q timed out", filename)
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

// DeleteLocalShards removes the shard directory for contentHash from the local
// shard store. Called when the file owner broadcasts a ShardDelete message.
func DeleteLocalShards(contentHash string) {
	shardDir := filepath.Join(ShardsDir(), contentHash)
	if err := os.RemoveAll(shardDir); err != nil {
		fmt.Printf("[Transfer] DeleteLocalShards: could not remove shards for %s: %v\n", contentHash, err)
		return
	}
	fmt.Printf("[Transfer] Deleted local shards for %s\n", contentHash)
}

// ──────────────────────────────────────────────────────────
// Shard redistribution
// ──────────────────────────────────────────────────────────

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
			if quicStream != nil {
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
			if quicStream != nil {
				transport = "QUIC"
			}
			fmt.Printf("[Transfer] Shard %d sent to %s (%d chunks, %s)\n", shardIndex, peerID[:8], chunkIndex+1, transport)
			return nil
		}
		if err != nil {
			return fmt.Errorf("read shard %d: %w", shardIndex, err)
		}
	}
}

// sendFrameViaQUIC writes a binary shard frame to a QUIC send stream using
// the 4-byte LE length-prefix framing that handleQUICStream expects.
func sendFrameViaQUIC(w io.WriteCloser, frame []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(frame)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(frame)
	return err
}

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

		if quicStream != nil {
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
	if quicStream != nil {
		transport = "QUIC"
	}
	fmt.Printf("[Transfer] Redistributed shard %d of %s → peer %s (%s)\n", shardIndex, fileHash[:12], peerID[:8], transport)
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

// encryptAndStoreShardFile reads srcPath in chunkSize windows, encrypts each chunk
// with AES-GCM, and writes the length-prefixed encrypted chunks to dstPath in the
// same on-disk format as writeEncryptedShardFile. Only one chunk (≤60 KB) is held
// in memory at a time, avoiding the 100-200 MB spike from loading an entire shard.
func encryptAndStoreShardFile(srcPath, dstPath string, key [32]byte) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	totalChunks := int((info.Size() + chunkSize - 1) / chunkSize)

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(totalChunks))
	if _, err := dst.Write(hdr[:]); err != nil {
		return err
	}

	buf := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			encrypted, eerr := encryptChunk(key, buf[:n])
			if eerr != nil {
				return eerr
			}
			var lenBuf [4]byte
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encrypted)))
			if _, err := dst.Write(lenBuf[:]); err != nil {
				return err
			}
			if _, err := dst.Write(encrypted); err != nil {
				return err
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
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
