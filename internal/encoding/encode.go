package encoding

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// filepath is relative to the storage dir where all the users drive files are stored
// essentially bin will be a copy of the filestructure just with shard folders at the base
// instead of files
func (e *Encoder) EncodeFile(relativeFilePath string) error {
	shardOutDir := filepath.Join(e.dirOut, ".bin", relativeFilePath)
	encodeFilePath := filepath.Join(e.dirOut, relativeFilePath)
	fileName := filepath.Base(relativeFilePath)
	fileName = fileName[:len(fileName)-len(filepath.Ext(fileName))]

	if err := os.MkdirAll(shardOutDir, 0755); err != nil {
		return err
	}

	shardFiles := make([]*os.File, e.parity+e.shards)
	for i := range shardFiles {
		shardName := fmt.Sprintf("shard%d_%s.dat", i, fileName)
		filePath := filepath.Join(shardOutDir, shardName)
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}

		shardFiles[i] = file
		defer file.Close()
	}

	in, err := os.Open(encodeFilePath)
	if err != nil {
		return err
	}
	readBuffer := make([]byte, e.blockSize*e.shards)

	for {
		lastBitReadIndex, err := io.ReadFull(in, readBuffer)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			for i := lastBitReadIndex; i < len(readBuffer); i++ {
				readBuffer[i] = 0
			}
		} else if err != nil {
			return err
		}
		splitFile, err := e.encoder.Split(readBuffer)
		if err != nil {
			return err
		}

		e.encoder.Encode(splitFile)
		var shardWriters sync.WaitGroup
		for i := range len(shardFiles) {
			shardWriters.Add(1)
			go func(i int) {
				defer shardWriters.Done()
				if _, err := shardFiles[i].Write(splitFile[i]); err != nil {
					panic(err)
				}
			}(i)
		}

		shardWriters.Wait()
		if lastBitReadIndex < len(readBuffer) {
			break
		}

	}

	fmt.Printf("files encoded shards found: %s", shardOutDir)
	return nil
}
