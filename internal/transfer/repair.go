package transfer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/encoding"
)

// missingShardFiles returns the indices in [0,totalShards) that have no shard
// file on the local disk for fileHash.
func missingShardFiles(fileHash string, totalShards int) []int {
	dir := filepath.Join(ShardsDir(), fileHash)
	var missing []int
	for i := 0; i < totalShards; i++ {
		p := filepath.Join(dir, fmt.Sprintf("shard%d_%s.dat", i, fileHash))
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, i)
		}
	}
	return missing
}

// RepairShardFile regenerates any shard files missing from the local disk for
// fileHash, in the canonical encrypted on-disk format, when this node holds
// enough shards to reconstruct the file. Returns the shard indices regenerated.
//
// This is owner-only in practice: reconstruction decrypts the present shards to
// plaintext, which only succeeds with the file owner's shard key. A non-owner
// (wrong key) decrypts zero shards and this returns (nil, nil) without touching
// disk. It is also a cheap no-op when no shard file is missing (the common case
// for an uploader, which holds all shards) — so it is safe to call eagerly.
//
// Because RS reconstruction is deterministic and the re-encryption uses the
// coordinate-derived nonce, every regenerated shard is byte-identical to the
// original that was distributed at upload time.
func RepairShardFile(fileHash string) ([]int, error) {
	meta := FindShardMetaByHash(fileHash)
	if meta == nil {
		return nil, fmt.Errorf("repair: no meta for %s", ShortHash(fileHash))
	}

	missing := missingShardFiles(fileHash, meta.TotalShards)
	if len(missing) == 0 {
		return nil, nil // fully present locally — nothing to repair
	}

	key, err := shardEncryptionKey()
	if err != nil {
		return nil, err
	}

	// Decrypt the shards we currently hold to plaintext. Only the owner's key
	// succeeds; a wrong key yields zero shards and we must not (and cannot) repair.
	plainDir, err := os.MkdirTemp("", "mosaic-repair-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(plainDir)

	decrypted, err := decryptShardsToDir(fileHash, meta.TotalShards, key, plainDir)
	if err != nil {
		return nil, fmt.Errorf("repair: decrypt shards: %w", err)
	}
	if decrypted < meta.TotalDataShards {
		// Not the owner, or we lack a decryptable quorum — cannot reconstruct.
		return nil, nil
	}

	// Reconstruct the missing plaintext shard files from the present ones.
	blockSize := meta.BlockSize
	if blockSize <= 0 {
		blockSize = encoding.ComputeBlockSize(meta.FileSize, meta.TotalDataShards)
	}
	enc, err := encoding.NewEncoder(meta.TotalDataShards, meta.TotalShards-meta.TotalDataShards, plainDir, plainDir)
	if err != nil {
		return nil, fmt.Errorf("repair: encoder init: %w", err)
	}
	enc.SetBlockSize(blockSize)

	regen, err := enc.ReconstructShardFiles(plainDir, fileHash)
	if err != nil {
		return nil, fmt.Errorf("repair: reconstruct: %w", err)
	}

	// Re-encrypt each regenerated plaintext shard into the canonical encrypted
	// on-disk format. Deterministic nonces make these identical to the originals.
	shardDir := filepath.Join(ShardsDir(), fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		return nil, err
	}
	var repaired []int
	for _, idx := range regen {
		src := filepath.Join(plainDir, fileHash, fmt.Sprintf("shard%d_%s.dat", idx, fileHash))
		dst := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", idx, fileHash))
		if err := encryptAndStoreShardFile(src, dst, key, fileHash, idx); err != nil {
			fmt.Printf("[Repair] shard %d of %s: re-encrypt failed: %v\n", idx, ShortHash(fileHash), err)
			continue
		}
		repaired = append(repaired, idx)

		// Record that we now hold this shard again so the manifest reflects it.
		shardStoredCbMu.Lock()
		cb := shardStoredCb
		shardStoredCbMu.Unlock()
		if cb != nil {
			go cb(fileHash, idx)
		}
	}

	// Keep meta consistent (RS params unchanged; ensures blockSize is persisted).
	writeShardMeta(shardDir, ShardMeta{
		FileName:        meta.FileName,
		FileHash:        meta.FileHash,
		FileSize:        meta.FileSize,
		TotalDataShards: meta.TotalDataShards,
		TotalShards:     meta.TotalShards,
		BlockSize:       blockSize,
	})

	if len(repaired) > 0 {
		fmt.Printf("[Repair] Regenerated %d shard(s) for %s: %v\n", len(repaired), ShortHash(fileHash), repaired)
	}
	return repaired, nil
}

// ShortHash truncates a content hash for logging without panicking on a short or
// malformed value (some hashes arrive in untrusted peer messages). Returns the
// whole string when it is shorter than the 12-char prefix.
func ShortHash(h string) string {
	if len(h) < 12 {
		return h
	}
	return h[:12]
}
