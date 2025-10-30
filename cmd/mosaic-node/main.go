package main

import (
	"fmt"
	"log"
	"os"

	"github.com/hcp-uw/mosaic/internal/encoding"
)

func main() {
	//for testing purposes rn
	file, err := os.ReadFile("./files/pic.jpg")

	if err != nil {
		log.Fatal(err)
	}

	fileSize := len(file)

	encoder, err := encoding.NewEncoder(8, 4, "./files")

	if err != nil {
		log.Fatal(err)
	}

	data, err := encoder.EncodeFile("/pic.jpg")

	// oh no 3 shards are missing!

	data[2] = nil
	data[4] = nil
	data[8] = nil

	if err != nil {
		log.Fatal(err)
	}

	err = encoder.DecodeShards(data, fileSize)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("welcome to mosaic")
}
