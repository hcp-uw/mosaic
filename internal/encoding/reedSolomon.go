package encoding

import (
	"errors"
	"math"
	"os"
)

// ts function is the main encode function -- will return a list of files which are the shards
func RSEncode(path string, shards int, parity int) ([]*os.File, error) {

	fileToEncode, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	brokenFile, err := fileSplit(fileToEncode, shards)
	if err != nil {
		return nil, err
	}

	return nil, errors.New("temp")

}

// splits file into k shards
func fileSplit(file []byte, shards int) ([][]byte, error) {

	if shards <= 0 {
		return nil, errors.New("shard count has to be greater than 0")
	}

	if len(file) == 0 {
		return nil, errors.New("File length cannot be 0")
	}

	shardSize := int(math.Ceil(float64(len(file)) / float64(shards)))

	var splitFile [][]byte

	for i := 0; i < shards; i++ {
		currentChunkStart := shardSize * i
		currentChunkEnd := (shardSize * i) + shardSize

		if currentChunkEnd > len(file) {
			currentChunkEnd = len(file)
		}

		currentChunk := file[currentChunkStart:currentChunkEnd]
		splitFile = append(splitFile, currentChunk)
	}

	return splitFile, nil
}
