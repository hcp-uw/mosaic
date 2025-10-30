package encoding

import (
	"errors"
	"log"
	"math"
	"os"
)

var exp [512]byte
var logTable [256]byte

const primitive = byte(0x1D)

func init() {
	initGFTables()
	log.Println(exp)
}

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
	log.Println(brokenFile)

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

	for i := range shards {
		currentChunkStart := shardSize * i
		currentChunkEnd := min(currentChunkStart+shardSize, len(file))

		currentChunk := file[currentChunkStart:currentChunkEnd]
		splitFile = append(splitFile, currentChunk)
	}

	return splitFile, nil
}

// for RS encoding algebraic operations need to be definined in a finite field
// basically if two bytes are added together they dont go over the byte limit or 255
// for some reason finite field is notated to GF (galois field)

// adds a gf
func gfAdd(a byte, b byte) byte {
	return a ^ b
}

// multiplies a gf
func gfMultiply(a byte, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}

	logA := int(logTable[a])
	logB := int(logTable[b])
	log.Printf("logA: %d, logB: %d\n", logA, logB)

	sumLog := logA + logB

	return exp[sumLog]
}

// divides a gf not quite sure ts is necessary yet
func gfDivid(a byte, b byte) (byte, error) {
	if b == 0 {
		return 0, errors.New("divide by zero lookin ahh")
	}

	if a == 0 {
		return 0, nil
	}

	logA := int(logTable[a])
	logB := int(logTable[b])

	difLog := logA - logB

	if difLog < 0 {
		difLog += 255
	}

	return exp[difLog], nil
}

// when you raise gf to a power
func gfPow(a byte, p int) byte {
	if p == 0 {
		return 1
	}

	if a == 0 {
		return 0
	}

	exp := (p * int(logTable[a])) % 255

	return byte(exp)
}

// initializes gf tables to do super uber fast multiplication
func initGFTables() {
	x := 1

	for i := range 255 {
		exp[i] = byte(x)
		logTable[byte(x)] = byte(i)

		x <<= 1

		if (x & 0x100) != 0 {
			x ^= int(primitive)
		}
	}

	logTable[0] = 255

	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
}

// creates a vandermonde matrix in GF zone
func createMatrix(baseShards int, parityShards int) [][]byte {
	matrix := make([][]byte, baseShards+parityShards)

	for row := 0; row < baseShards+parityShards; row++ {
		matrix[row] = make([]byte, baseShards)
		for col := range baseShards {
			matrix[row][col] = gfPow(byte(row+1), col)
		}
	}

	return matrix
}
