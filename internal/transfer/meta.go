package transfer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/encoding"
)

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
