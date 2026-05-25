package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/encoding"
)

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

	// Skip if the shard is already on disk — avoids double-assembly when the
	// upload path and redistribution path both push the same shard concurrently.
	shardPath := filepath.Join(ShardsDir(), c.fileHash,
		fmt.Sprintf("shard%d_%s.dat", c.shardIndex, c.fileHash))
	if _, err := os.Stat(shardPath); err == nil {
		return
	}

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
			firstChunkAt:    time.Now(),
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
	if v, ok := shardActivityChans.Load(key); ok {
		ch := v.(chan struct{})
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	if received%100 == 0 || received == total {
		fmt.Printf("[Recv] Shard %d: %d/%d chunks\n", c.shardIndex, received, total)
	}

	if received == total {
		assemblyMu.Lock()
		finalAsm := assemblies[key]
		delete(assemblies, key)
		assemblyMu.Unlock()
		finalizingShards.Store(key, struct{}{})
		go func() {
			defer finalizingShards.Delete(key)
			finalizeShard(finalAsm)
		}()
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
	elapsed := time.Since(asm.firstChunkAt)
	fmt.Printf("[Transfer] Shard %d assembled in %.1fs → %s\n", asm.shardIndex, elapsed.Seconds(), shardPath)

	// Update download-progress counter so the UI shows correct shard counts.
	// StoreShardData does this for the whole-shard path; finalizeShard covers
	// the chunk-streaming path (HandleBinaryShardChunk → QUIC/UDP delivery).
	downloadTargetMu.Lock()
	if downloadTargetHash == asm.fileHash {
		downloadShardsReceived.Add(1)
	}
	downloadTargetMu.Unlock()

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
