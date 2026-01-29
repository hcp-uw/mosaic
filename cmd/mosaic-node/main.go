package main

import (
	"fmt"
	"log"
	"os"

	//"os"

	//"github.com/hcp-uw/mosaic/internal/cli"
	"github.com/hcp-uw/mosaic/internal/daemon"
	"github.com/hcp-uw/mosaic/internal/encoding"
)

func main() {
	if err := daemon.StartServer(); err != nil {
		panic(err)
	}
	fmt.Println("welcome to mosaic")

	file, err := os.ReadFile("output_file.jpg")
	if err != nil {
		log.Fatal(err)
	}

	fileSize := len(file)

	encoder, err := encoding.NewEncoder(8, 4, "./files", "./files/.bin")

	if err != nil {
		log.Fatal(err)
	}

	//err = encoder.EncodeFile("/pictures/pic.jpg")
	//if err != nil {
	//	fmt.Println(err)
	//}

	//fmt.Println(fileSize)

	err = encoder.DecodeShards("pictures/pic.jpg", fileSize)
	if err != nil {
		log.Fatal(err)
	}

}
