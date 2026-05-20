package transfer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/encoding"
)

// EnsureShardMeta writes or updates a meta.json for the given file. If no meta
// exists it creates one. If meta exists but is privacy-stripped (no fileName —
// written by a node acting as a courier for another user's shards), it fills in
// the fileName and fileSize now that we know we are the file owner. RS parameters
// from the existing meta are preserved so serving and reconstruction stay correct.
func EnsureShardMeta(fileHash, fileName string, fileSize int) {
	existing := FindShardMetaByHash(fileHash)
	if existing != nil && existing.FileName != "" {
		return // meta is already complete
	}
	shardDir := filepath.Join(ShardsDir(), fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		return
	}
	m := ShardMeta{
		FileName:        fileName,
		FileHash:        fileHash,
		FileSize:        fileSize,
		TotalDataShards: DataShards,
		TotalShards:     TotalShards,
		BlockSize:       encoding.ComputeBlockSize(fileSize, DataShards),
	}
	if existing != nil {
		// Preserve RS parameters written during shard assembly — they may differ
		// from the defaults if the file was encoded with custom shard counts.
		if existing.TotalDataShards > 0 {
			m.TotalDataShards = existing.TotalDataShards
		}
		if existing.TotalShards > 0 {
			m.TotalShards = existing.TotalShards
		}
		if existing.BlockSize > 0 {
			m.BlockSize = existing.BlockSize
		}
	}
	writeShardMeta(shardDir, m)
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

// UpdateShardMetaFileName rewrites the FileName field in the meta.json for
// the shard set identified by fileHash. Call this after a network rename so
// that StreamShardToPeer and FetchFileBytes use the new filename.
func UpdateShardMetaFileName(fileHash, newName string) {
	shardDir := filepath.Join(ShardsDir(), fileHash)
	data, err := os.ReadFile(filepath.Join(shardDir, "meta.json"))
	if err != nil {
		return // no shards stored locally, nothing to update
	}
	var m ShardMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	m.FileName = newName
	writeShardMeta(shardDir, m)
}

// StripAllShardMetaFileInfo rewrites every meta.json under ShardsDir to clear
// FileName and FileSize so that the next account cannot read the previous
// account's file names from shard metadata. RS parameters are preserved so
// shards can still be served to peers as an anonymous courier.
func StripAllShardMetaFileInfo() {
	entries, err := os.ReadDir(ShardsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		shardDir := filepath.Join(ShardsDir(), e.Name())
		data, err := os.ReadFile(filepath.Join(shardDir, "meta.json"))
		if err != nil {
			continue
		}
		var m ShardMeta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.FileName == "" && m.FileSize == 0 {
			continue // already stripped
		}
		m.FileName = ""
		m.FileSize = 0
		writeShardMeta(shardDir, m)
	}
}

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
