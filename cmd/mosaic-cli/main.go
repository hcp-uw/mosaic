package main

import (
	"fmt"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli"
)

func main() {
	if len(os.Args) > 1 {
		cli.Run(os.Args)
		return
	}

	fmt.Println("Welcome to Mosaic")
}
