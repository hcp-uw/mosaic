package encoding

import (
	"errors"
	"os"

	"github.com/klauspost/reedsolomon"
)

type Encoder struct {
	encoder   reedsolomon.Encoder
	shards    int
	parity    int
	blockSize int

	dirOut string
	dirIn  string
}

func NewEncoder(dataShards int, parityShards int, outPath string, inPath string) (*Encoder, error) {

	if dataShards <= 0 || parityShards <= 0 {
		return nil, errors.New("Shard counts have to be greater than or equal to 0")
	}

	encoder, err := reedsolomon.New(dataShards, parityShards)

	if err != nil {
		return nil, err
	}

	if err := checkDirectory(inPath); err != nil {
		return nil, err
	}
	if err := checkDirectory(outPath); err != nil {
		return nil, err
	}

	newEncoder := &Encoder{
		encoder: encoder,
		shards:  dataShards,
		parity:  parityShards,
		// blockSize is 0 by default; EncodeFile computes the correct value
		// from the actual file size before encoding begins.
		blockSize: 0,

		dirOut: outPath,
		dirIn:  inPath,
	}

	return newEncoder, nil
}

// BlockSize returns the shard block size currently set on the encoder.
// This is 0 until EncodeFile is called, which sets it based on file size.
func (e *Encoder) BlockSize() int { return e.blockSize }

// SetBlockSize overrides the block size. Call this before DecodeShards
// using the value stored in ShardMeta.BlockSize.
func (e *Encoder) SetBlockSize(n int) { e.blockSize = n }

// ComputeBlockSize returns the appropriate shard block size for a file of
// fileSize bytes split across dataShards data shards.
// Formula: ceil(fileSize / dataShards), capped at 20 MB for very large files.
func ComputeBlockSize(fileSize, dataShards int) int {
	if fileSize <= 0 || dataShards <= 0 {
		return 1
	}
	bs := (fileSize + dataShards - 1) / dataShards
	const maxBlock = 20 * 1024 * 1024 // 20 MB cap for large files
	return max(1, min(bs, maxBlock))
}

func checkDirectory(path string) error {
	info, err := os.Stat(path)

	if os.IsNotExist(err) {
		return errors.New("This directory doesn't exist")
	}

	if !info.IsDir() {
		return errors.New("Path exists but ts is not a directory")
	}

	if err != nil {
		return err
	}
	return nil
}


