package encoding

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// EncodeFile RS-encodes a file and writes the shard files to the output directory.
// filepath is relative to the storage dir where all the users drive files are stored.
func (e *Encoder) EncodeFile(relativeFilePath string) error {
	_, err := e.encodeFile(relativeFilePath, nil)
	return err
}

// EncodeFileAndHash RS-encodes a file and simultaneously computes its SHA-256
// hash via a TeeReader, returning the hex hash. This avoids the separate full
// file read that a standalone hash pass would require.
func (e *Encoder) EncodeFileAndHash(relativeFilePath string) (string, error) {
	return e.encodeFile(relativeFilePath, sha256.New())
}

func (e *Encoder) encodeFile(relativeFilePath string, h hash.Hash) (string, error) {
	shardOutDir := filepath.Join(e.dirOut, ".bin", relativeFilePath)
	encodeFilePath := filepath.Join(e.dirOut, relativeFilePath)
	fileName := filepath.Base(relativeFilePath)
	fileName = fileName[:len(fileName)-len(filepath.Ext(fileName))]

	if err := os.MkdirAll(shardOutDir, 0755); err != nil {
		return "", err
	}

	// Compute block size from the actual file size so small files don't get
	// padded to a fixed 20 MB shard size.
	info, err := os.Stat(encodeFilePath)
	if err != nil {
		return "", err
	}
	e.blockSize = ComputeBlockSize(int(info.Size()), e.shards)

	shardFiles := make([]*os.File, e.parity+e.shards)
	for i := range shardFiles {
		shardName := fmt.Sprintf("shard%d_%s.dat", i, fileName)
		filePath := filepath.Join(shardOutDir, shardName)
		file, err := os.Create(filePath)
		if err != nil {
			return "", err
		}
		shardFiles[i] = file
		defer file.Close()
	}

	in, err := os.Open(encodeFilePath)
	if err != nil {
		return "", err
	}
	defer in.Close()

	var src io.Reader = in
	if h != nil {
		src = io.TeeReader(in, h)
	}
	readBuffer := make([]byte, e.blockSize*e.shards)

	for {
		lastBitReadIndex, err := io.ReadFull(src, readBuffer)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			for i := lastBitReadIndex; i < len(readBuffer); i++ {
				readBuffer[i] = 0
			}
		} else if err != nil {
			return "", err
		}
		splitFile, err := e.encoder.Split(readBuffer)
		if err != nil {
			return "", err
		}

		e.encoder.Encode(splitFile)

		var writeErr error
		var mu sync.Mutex
		var shardWriters sync.WaitGroup
		for i := range len(shardFiles) {
			shardWriters.Add(1)
			go func(i int) {
				defer shardWriters.Done()
				if _, err := shardFiles[i].Write(splitFile[i]); err != nil {
					mu.Lock()
					writeErr = err
					mu.Unlock()
				}
			}(i)
		}
		shardWriters.Wait()
		if writeErr != nil {
			return "", writeErr
		}

		if lastBitReadIndex < len(readBuffer) {
			break
		}
	}

	fmt.Printf("encoded %s → %s\n", relativeFilePath, shardOutDir)
	if h != nil {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	return "", nil
}
