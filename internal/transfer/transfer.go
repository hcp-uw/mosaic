package transfer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
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
)

// ShardsDir returns ~/Mosaic/.shards — the base directory for all stored shards.
func ShardsDir() string {
	return filepath.Join(os.Getenv("HOME"), "Mosaic", ".shards")
}

// ──────────────────────────────────────────────────────────
// Shard encryption key
// ──────────────────────────────────────────────────────────

// shardEncryptionKey derives a 32-byte AES-256 key from the user's login key
// using HKDF-SHA256. Same login key on every device → same derived shard key,
// so all nodes in the network can encrypt and decrypt each other's shards.
func shardEncryptionKey() ([32]byte, error) {
	var key [32]byte
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".mosaic-login.key"))
	if err != nil {
		return key, fmt.Errorf("not logged in — run 'mos login account <username> <key>'")
	}
	loginKey := strings.TrimSpace(string(data))
	if loginKey == "" {
		return key, fmt.Errorf("login key is empty")
	}
	derived, err := hkdf.Key(sha256.New, []byte(loginKey), nil, "mosaic-shard-key", 32)
	if err != nil {
		return key, fmt.Errorf("key derivation failed: %w", err)
	}
	copy(key[:], derived)
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

	// Persist shards locally so the uploader can reconstruct its own files later.
	shardDir := filepath.Join(ShardsDir(), fileHash)
	if err := os.MkdirAll(shardDir, 0755); err == nil {
		for i := 0; i < TotalShards; i++ {
			src := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", i, nameNoExt))
			dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
			_ = copyFile(src, dst)
		}
		writeShardMeta(shardDir, ShardMeta{
			FileName:        filename,
			FileHash:        fileHash,
			FileSize:        fileSize,
			TotalDataShards: DataShards,
			TotalShards:     TotalShards,
		})
	}

	if client == nil || !client.IsPeerCommunicationAvailable() {
		fmt.Println("[Transfer] No peers connected — shards saved locally only")
		return
	}

	fmt.Println("[Transfer] Sending shards to peers…")

	var wg sync.WaitGroup
	var sendErr error
	var sendErrMu sync.Mutex
	sem := make(chan struct{}, 1) // sequential: one shard at a time

	for i := 0; i < TotalShards; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(shardIdx int) {
			defer func() { <-sem }()
			defer wg.Done()

			shardPath := filepath.Join(outDir, ".bin", filename, fmt.Sprintf("shard%d_%s.dat", shardIdx, nameNoExt))
			info, err := os.Stat(shardPath)
			if err != nil {
				sendErrMu.Lock()
				sendErr = fmt.Errorf("cannot stat shard %d: %w", shardIdx, err)
				sendErrMu.Unlock()
				return
			}
			totalChunks := int((info.Size() + chunkSize - 1) / chunkSize)

			sf, err := os.Open(shardPath)
			if err != nil {
				sendErrMu.Lock()
				sendErr = fmt.Errorf("cannot open shard %d: %w", shardIdx, err)
				sendErrMu.Unlock()
				return
			}
			defer sf.Close()

			buf := make([]byte, chunkSize)
			chunkIndex := 0
			for {
				n, err := io.ReadFull(sf, buf)
				if n > 0 {
					encrypted, eerr := encryptChunk(netKey, buf[:n])
					if eerr != nil {
						sendErrMu.Lock()
						sendErr = fmt.Errorf("encrypt failed shard %d chunk %d: %w", shardIdx, chunkIndex, eerr)
						sendErrMu.Unlock()
						return
					}
					frame, ferr := encodeBinaryShardChunk(binaryShardChunk{
						fileHash:        fileHash,
						fileName:        filename,
						fileSize:        fileSize,
						shardIndex:      shardIdx,
						chunkIndex:      chunkIndex,
						totalChunks:     totalChunks,
						totalDataShards: DataShards,
						totalShards:     TotalShards,
						data:            encrypted,
					})
					if ferr != nil {
						sendErrMu.Lock()
						sendErr = fmt.Errorf("encode frame shard %d chunk %d: %w", shardIdx, chunkIndex, ferr)
						sendErrMu.Unlock()
						return
					}
					<-sendLimiter
					if werr := client.SendRawToAllPeers(frame); werr != nil {
						sendErrMu.Lock()
						sendErr = fmt.Errorf("send failed shard %d chunk %d: %w", shardIdx, chunkIndex, werr)
						sendErrMu.Unlock()
						return
					}
					chunkIndex++
				}
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					break
				}
				if err != nil {
					sendErrMu.Lock()
					sendErr = fmt.Errorf("read error shard %d: %w", shardIdx, err)
					sendErrMu.Unlock()
					return
				}
			}
			fmt.Printf("[Transfer] Shard %d/%d sent (%d chunks)\n", shardIdx+1, TotalShards, chunkIndex)
		}(i)
	}

	wg.Wait()
	if sendErr != nil {
		fmt.Printf("[Transfer] Upload incomplete: %v\n", sendErr)
		return
	}
	fmt.Printf("[Transfer] Upload complete: %s\n", filename)
}

// ──────────────────────────────────────────────────────────
// Receive
// ──────────────────────────────────────────────────────────

// HandleBinaryShardChunk processes a raw binary shard frame received from a peer.
// Called directly from the message router when data[0] == binaryMagic.
func HandleBinaryShardChunk(data []byte) {
	c, err := decodeBinaryShardChunk(data)
	if err != nil {
		fmt.Printf("[Transfer] Bad binary frame: %v\n", err)
		return
	}

	netKey, err := shardEncryptionKey()
	if err != nil {
		fmt.Printf("[Transfer] Cannot derive shard key: %v\n", err)
		return
	}
	plain, err := decryptChunk(netKey, c.data)
	if err != nil {
		fmt.Printf("[Transfer] Cannot decrypt shard %d chunk %d: %v\n", c.shardIndex, c.chunkIndex, err)
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
	asm.chunks[c.chunkIndex] = plain
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

	shardPath := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", asm.shardIndex, asm.fileHash))
	sf, err := os.Create(shardPath)
	if err != nil {
		fmt.Printf("[Transfer] Cannot create shard file: %v\n", err)
		return
	}
	for i := 0; i < asm.totalChunks; i++ {
		chunk, ok := asm.chunks[i]
		if !ok {
			sf.Close()
			fmt.Printf("[Transfer] Missing chunk %d for shard %d\n", i, asm.shardIndex)
			return
		}
		if _, err := sf.Write(chunk); err != nil {
			sf.Close()
			fmt.Printf("[Transfer] Write failed at chunk %d: %v\n", i, err)
			return
		}
	}
	sf.Close()
	fmt.Printf("[Transfer] Shard %d assembled → %s\n", asm.shardIndex, shardPath)

	writeShardMeta(shardDir, ShardMeta{
		FileName:        asm.fileName,
		FileHash:        asm.fileHash,
		FileSize:        asm.fileSize,
		TotalDataShards: asm.totalDataShards,
		TotalShards:     asm.totalShards,
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
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	outDir, err := os.MkdirTemp("", "mosaic-recon-*")
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: cannot create output dir: %v\n", err)
		return
	}
	defer os.RemoveAll(outDir)

	enc, err := encoding.NewEncoder(asm.totalDataShards, asm.totalShards-asm.totalDataShards, outDir, ShardsDir())
	if err != nil {
		fmt.Printf("[Transfer] Reconstruct: encoder init failed: %v\n", err)
		return
	}
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
}

// ──────────────────────────────────────────────────────────
// Download
// ──────────────────────────────────────────────────────────

// FetchFileBytes reconstructs the original file bytes from locally stored shards.
func FetchFileBytes(filename string) ([]byte, error) {
	shardsBase := ShardsDir()

	entries, err := os.ReadDir(shardsBase)
	if err != nil {
		return nil, fmt.Errorf("shards not available yet (no shards directory)")
	}

	var meta *ShardMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(shardsBase, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var m ShardMeta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.FileName == filename {
			meta = &m
			break
		}
	}
	if meta == nil {
		return nil, fmt.Errorf("shards for %q not found — file may not have been received yet", filename)
	}

	shardDir := filepath.Join(shardsBase, meta.FileHash)
	for i := 0; i < meta.TotalDataShards; i++ {
		p := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, meta.FileHash))
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("shard %d missing — cannot reconstruct %s", i, filename)
		}
	}

	outDir, err := os.MkdirTemp("", "mosaic-fetch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	enc, err := encoding.NewEncoder(meta.TotalDataShards, meta.TotalShards-meta.TotalDataShards, outDir, shardsBase)
	if err != nil {
		return nil, fmt.Errorf("encoder init: %w", err)
	}
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
