package main

import (
	"fmt"

	//"github.com/hcp-uw/mosaic/internal/cli"
	"github.com/hcp-uw/mosaic/internal/daemon"
)

func main() {

	if err := daemon.StartServer(); err != nil {
		panic(err)
	}

	fmt.Println("welcome to mosaic")
}
