package transfer

import (
	"context"
	"crypto/sha256"
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
	chunkSize    = 4 * 1024 // 4 KB raw → ~6 KB JSON/base64, safely under macOS UDP max
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
	fileName        string // full name with extension
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

// UploadFile RS-encodes path, saves shards locally, and streams them to all
// connected peers. Runs synchronously; wrap in a goroutine for background use.
func UploadFile(path string, client *p2p.Client) {
	filename := filepath.Base(path)
	nameNoExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Hash and measure the file (streaming, no full load into memory).
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

	fmt.Printf("[Transfer] Uploading %s  hash=%s…  size=%d bytes\n", filename, fileHash[:12], fileSize)

	outDir, err := os.MkdirTemp("", "mosaic-upload-*")
	if err != nil {
		fmt.Printf("[Transfer] Cannot create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(outDir)

	// Copy original into temp dir for the encoder to read.
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

	// Persist shards locally so the uploader can reconstruct its own files.
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
	sign := api.NewSignature(client.GetID())

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
			shardFileSize := info.Size()
			totalChunks := int((shardFileSize + chunkSize - 1) / chunkSize)

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
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					msg := api.NewShardChunkMessage(sign, api.ShardChunkData{
						FileHash:        fileHash,
						FileName:        filename, // full name with extension
						FileSize:        fileSize,
						ShardIndex:      shardIdx,
						ChunkIndex:      chunkIndex,
						TotalChunks:     totalChunks,
						TotalDataShards: DataShards,
						TotalShards:     TotalShards,
						Data:            chunk,
					})
					<-sendLimiter // token bucket rate cap
					if werr := client.SendToAllPeers(msg); werr != nil {
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

// HandleShardChunk processes an incoming ShardChunk message from a peer.
func HandleShardChunk(msg *api.Message) {
	d, err := msg.GetShardChunkData()
	if err != nil {
		fmt.Printf("[Transfer] Failed to parse ShardChunk: %v\n", err)
		return
	}

	key := fmt.Sprintf("%s:%d", d.FileHash, d.ShardIndex)

	assemblyMu.Lock()
	asm, ok := assemblies[key]
	if !ok {
		asm = &shardAssembly{
			chunks:          make(map[int][]byte),
			totalChunks:     d.TotalChunks,
			fileName:        d.FileName,
			fileHash:        d.FileHash,
			fileSize:        d.FileSize,
			shardIndex:      d.ShardIndex,
			totalDataShards: d.TotalDataShards,
			totalShards:     d.TotalShards,
		}
		assemblies[key] = asm
	}
	assemblyMu.Unlock()

	asm.mu.Lock()
	asm.chunks[d.ChunkIndex] = d.Data
	received := len(asm.chunks)
	total := asm.totalChunks
	asm.mu.Unlock()

	if received%100 == 0 || received == total {
		fmt.Printf("[Recv] Shard %d: %d/%d chunks\n", d.ShardIndex, received, total)
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

	// Always (over)write meta so the filename is correct.
	writeShardMeta(shardDir, ShardMeta{
		FileName:        asm.fileName,
		FileHash:        asm.fileHash,
		FileSize:        asm.fileSize,
		TotalDataShards: asm.totalDataShards,
		TotalShards:     asm.totalShards,
	})

	// Trigger file reconstruction once all data shards are on disk.
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

// autoReconstruct decodes all data shards and writes the recovered file directly
// to ~/Mosaic/<originalFilename> so it appears in the user's Mosaic directory.
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

// FetchFileBytes reconstructs the original file bytes from locally stored shards.
// Scans ShardsDir for a meta.json matching filename, then runs RS decoding.
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

// helpers

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
