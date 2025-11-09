package encoding

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// has to remember file length
// relativePath is the path inside the IN folder where incoming shards are stored
// should be a directory not a file
func (e *Encoder) DecodeShards(relativePath string, fileLength int) error {
	totalShards := e.parity + e.shards
	fileName := filepath.Base(relativePath)
	fileNameNoExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	type shardResult struct {
		index  int
		result []byte
	}

	shardFiles := make([]*os.File, totalShards)
	for i := 0; i < totalShards; i++ {
		shardName := fmt.Sprintf("shard%d_%s.dat", i, fileNameNoExt)
		shardPath := filepath.Join(e.dirIn, relativePath, shardName)

		file, err := os.Open(shardPath)
		if err != nil {
			shardFiles[i] = nil
			continue
		}
		shardFiles[i] = file
	}

	// makes sure outpath exists if not it creates it
	outPath := filepath.Dir(filepath.Join(e.dirOut, relativePath))
	err := os.MkdirAll(outPath, 0755)
	if err != nil {
		log.Fatal("here")
		return err
	}

	// on first run through it finds the file extension creates the file then further
	// writes to that file
	var outFile *os.File
	writtenBytes := 0
	fileCreated := false
	for {
		if fileLength <= writtenBytes {
			break
		}

		shardResults := make(chan shardResult, totalShards)
		shardArray := make([][]byte, totalShards)
		var shardReaders sync.WaitGroup

		for index, file := range shardFiles {
			if file == nil {
				continue
			}

			shardReaders.Add(1)
			go func(index int, file *os.File) {
				defer shardReaders.Done()
				shard := make([]byte, e.blockSize)

				io.ReadFull(file, shard)
				// ts silent error is not that tuff

				shardResults <- shardResult{index: index, result: shard}

			}(index, file)
		}

		go func() {
			shardReaders.Wait()
			close(shardResults)
		}()

		var dataReadCount int
		for res := range shardResults {
			shardArray[res.index] = res.result
			if res.result != nil {
				dataReadCount++
			}
		}

		// CRITICAL FIX: Exit condition 2: If zero non-nil blocks were read in this iteration,
		// it means all active shards are exhausted. Break the decoding loop to prevent
		// continuous reconstruction failure on empty data.
		if dataReadCount == 0 {
			break
		}

		err := e.encoder.ReconstructData(shardArray)
		if err != nil {
			return err
		}

		var buf bytes.Buffer

		// works except for padding
		err = e.encoder.Join(&buf, shardArray, e.shards*e.blockSize)
		joinedBytes := buf.Bytes()
		if !fileCreated {

			fileExtension := detectExtension(joinedBytes)
			fileOutDir := filepath.Join(e.dirOut, filepath.Dir(relativePath))
			fileOutPath := filepath.Join(fileOutDir, fileNameNoExt+fileExtension)
			os.MkdirAll(fileOutDir, 0755)

			outFile, err = os.Create(fileOutPath)
			if err != nil {
				return err
			}
			defer outFile.Close()

			fileCreated = true
		}

		remaining := fileLength - writtenBytes
		toWrite := len(joinedBytes)
		if remaining < toWrite {
			toWrite = remaining
		}

		totalWritten := 0
		for totalWritten < toWrite {
			n, err := outFile.Write(joinedBytes[totalWritten:toWrite])
			if err != nil {
				return err
			}
			totalWritten += n
			writtenBytes += n
		}
	}

	return nil
}

func detectExtension(data []byte) string {
	if len(data) > 512 {
		data = data[:512] // only need first 512 bytes
	}
	mimeType := http.DetectContentType(data)

	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "application/pdf":
		return ".pdf"
	case "text/plain; charset=utf-8":
		return ".txt"
	default:
		return ".bin" // fallback
	}
}
