package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/daemon"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

func main() {
	mountPoint := filepath.Join(os.Getenv("HOME"), "Mosaic")

	if err := filesystem.StartMount(mountPoint); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create Mosaic directory: %v\n", err)
	} else {
		fmt.Printf("Mosaic directory ready at %s\n", mountPoint)

		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			<-c
			fmt.Println("Shutting down Mosaic...")
			filesystem.StopMount(mountPoint)
			os.Exit(0)
		}()
	}

	// HTTP API for the Finder Sync extension and other UI bridges.
	go func() {
		if err := daemon.StartHTTPServer(); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
		}
	}()

	// Unix socket server (blocks until daemon exits).
	if err := daemon.StartServer(); err != nil {
		filesystem.StopMount(mountPoint)
		panic(err)
	}
}
