package main

import (
	"fmt"
	//"os"

	//"github.com/hcp-uw/mosaic/internal/cli"
	"github.com/hcp-uw/mosaic/internal/daemon"
)

func main() {
	//if len(os.Args) > 1 {
	//	cli.Run(os.Args)
	//	return
	//}
	if err := daemon.StartServer(); err != nil {
		panic(err)
	}
	fmt.Println("welcome to mosaic")
}
