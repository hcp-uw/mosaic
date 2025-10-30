package encoding

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/klauspost/reedsolomon"
)

type Encoder struct {
	encoder reedsolomon.Encoder
	shards  int
	parity  int

	dirPath string
}

func NewEncoder(dataShards int, parityShards int, path string) (*Encoder, error) {

	if dataShards <= 0 || parityShards <= 0 {
		return nil, errors.New("Shard counts have to be greater than or equal to 0")
	}

	encoder, err := reedsolomon.New(dataShards, parityShards)

	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)

	if os.IsNotExist(err) {
		return nil, errors.New("This directory doesn't exist")
	}

	if !info.IsDir() {
		return nil, errors.New("Path exists but ts is not a directory")
	}

	newEncoder := &Encoder{
		encoder: encoder,
		shards:  dataShards,
		parity:  parityShards,

		dirPath: path,
	}

	return newEncoder, nil
}

// ts function encodes the file while having all of it stored in memory - sheeesh (will fix)
func (e *Encoder) EncodeFile(relativeFilePath string) ([][]byte, error) {

	fileToEncode, err := os.ReadFile(e.dirPath + relativeFilePath)
	if err != nil {
		return nil, err
	}

	splitFile, err := e.encoder.Split(fileToEncode)
	if err != nil {
		return nil, err
	}

	err = e.encoder.Encode(splitFile)
	if err != nil {
		return nil, err
	}

	return splitFile, nil
}

// right now ts has to remember file length will fix later
func (e *Encoder) DecodeShards(shards [][]byte, fileLength int) error {
	if len(shards) < e.shards {
		return errors.New("Not enough data to reconstruct")
	}
	data := make([][]byte, e.shards+e.parity)

	// Copy existing shards
	for i := range shards {
		data[i] = make([]byte, len(shards[i]))
		copy(data[i], shards[i])
	}

	// Initialize empty parity shards
	for i := len(shards); i < e.shards+e.parity; i++ {
		data[i] = make([]byte, len(shards[0]))
	}

	err := e.encoder.ReconstructData(data)

	if err != nil {
		return err
	}

	var buf bytes.Buffer

	err = e.encoder.Join(&buf, data, fileLength)
	if err != nil {
		log.Println("here")
		return err
	}

	joinedBytes := buf.Bytes()

	fileExtension := detectExtension(joinedBytes)
	outFile, err := os.Create("output_file" + fileExtension)
	if err != nil {
		return err
	}

	defer outFile.Close()

	_, err = outFile.Write(joinedBytes)

	log.Println("File written to ./output_file")

	return err
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
