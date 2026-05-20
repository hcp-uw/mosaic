package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

func main() {
	mountPoint := shared.MosaicDir()

	if err := filesystem.StartMount(mountPoint); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create Mosaic directory: %v\n", err)
	} else {
		fmt.Printf("Mosaic directory ready at %s\n", mountPoint)

		// Ensure both manifest files exist on disk before anything else starts.
		// This prevents races where concurrent ManifestSync messages all try to
		// create the file at the same time on a fresh node.
		if err := filesystem.EnsureManifest(mountPoint); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not initialise local manifest: %v\n", err)
		}
		if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
			if err := filesystem.EnsureNetworkManifest(mountPoint, aesKey); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not initialise network manifest: %v\n", err)
			}
		}

		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			<-c
			fmt.Println("Shutting down Mosaic...")
			if client := handlers.GetP2PClient(); client != nil {
				_ = client.DisconnectFromStun()
				// NodeLeave is sent over UDP; give the kernel time to flush the
				// send buffer before os.Exit tears down the socket.
				time.Sleep(300 * time.Millisecond)
				handlers.SetP2PClient(nil)
			}
			filesystem.StopMount(mountPoint)
			os.Exit(0)
		}()
	}

	// Watch ~/Mosaic/ and map filesystem events to network operations.
	if _, err := daemon.StartDirWatcher(mountPoint); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not start dir watcher: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
		os.Exit(1)
	}
}
