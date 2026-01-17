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
		// essentially the maximum amount of bytes in one individual dataShards
		// the total amount of bytes in ram will be #dataShard * blockSize
		blockSize: 20 * 1024 * 1024,

		dirOut: outPath,
		dirIn:  inPath,
	}

	return newEncoder, nil
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


