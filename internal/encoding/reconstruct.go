package encoding

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ReconstructShardFiles rebuilds any missing plaintext shard files in dir/<name>/
// from the present ones using Reed-Solomon reconstruction, writing each
// regenerated shard file back alongside the present ones. It reconstructs the
// FULL shard set (data + parity), so any missing index is restored as long as at
// least dataShards of the totalShards files are present.
//
// Shard files are named shard<i>_<name>.dat and hold the raw RS block stream
// (numStripes × blockSize bytes). SetBlockSize must have been called with the
// same block size used at encode time. Returns the indices that were regenerated.
//
// Because RS encoding is deterministic for a fixed (dataShards, parityShards,
// blockSize, input), a regenerated shard is byte-identical to the original — so
// after the caller re-encrypts it with the deterministic per-chunk nonce, the
// repaired shard matches every other copy on the network.
func (e *Encoder) ReconstructShardFiles(dir, name string) ([]int, error) {
	if e.blockSize <= 0 {
		return nil, errors.New("ReconstructShardFiles: block size not set")
	}
	totalShards := e.shards + e.parity
	shardDir := filepath.Join(dir, name)

	paths := make([]string, totalShards)
	present := make([]bool, totalShards)
	presentCount := 0
	numStripes := 0
	for i := 0; i < totalShards; i++ {
		paths[i] = filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, name))
		info, err := os.Stat(paths[i])
		if err != nil || info.Size() == 0 {
			continue
		}
		if info.Size()%int64(e.blockSize) != 0 {
			return nil, fmt.Errorf("ReconstructShardFiles: shard %d size %d not a multiple of block size %d", i, info.Size(), e.blockSize)
		}
		stripes := int(info.Size()) / e.blockSize
		if numStripes == 0 {
			numStripes = stripes
		} else if stripes != numStripes {
			return nil, fmt.Errorf("ReconstructShardFiles: shard %d has %d stripes, expected %d", i, stripes, numStripes)
		}
		present[i] = true
		presentCount++
	}

	if presentCount < e.shards {
		return nil, fmt.Errorf("ReconstructShardFiles: only %d of %d shards present — need at least %d", presentCount, totalShards, e.shards)
	}
	if numStripes == 0 {
		return nil, errors.New("ReconstructShardFiles: could not determine stripe count")
	}

	readers := make([]*os.File, totalShards)
	writers := make([]*os.File, totalShards)
	var regenerated []int
	closeAll := func() {
		for _, f := range readers {
			if f != nil {
				f.Close()
			}
		}
		for _, f := range writers {
			if f != nil {
				f.Close()
			}
		}
	}
	for i := 0; i < totalShards; i++ {
		if present[i] {
			f, err := os.Open(paths[i])
			if err != nil {
				closeAll()
				return nil, err
			}
			readers[i] = f
		} else {
			f, err := os.Create(paths[i])
			if err != nil {
				closeAll()
				return nil, err
			}
			writers[i] = f
			regenerated = append(regenerated, i)
		}
	}
	defer closeAll()

	for stripe := 0; stripe < numStripes; stripe++ {
		blocks := make([][]byte, totalShards)
		for i := 0; i < totalShards; i++ {
			if !present[i] {
				continue // leave nil for Reconstruct to fill
			}
			buf := make([]byte, e.blockSize)
			if _, err := io.ReadFull(readers[i], buf); err != nil {
				return nil, fmt.Errorf("ReconstructShardFiles: read shard %d stripe %d: %w", i, stripe, err)
			}
			blocks[i] = buf
		}
		if err := e.encoder.Reconstruct(blocks); err != nil {
			return nil, fmt.Errorf("ReconstructShardFiles: reconstruct stripe %d: %w", stripe, err)
		}
		for _, i := range regenerated {
			if _, err := writers[i].Write(blocks[i]); err != nil {
				return nil, fmt.Errorf("ReconstructShardFiles: write shard %d stripe %d: %w", i, stripe, err)
			}
		}
	}
	return regenerated, nil
}
