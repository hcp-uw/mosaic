package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// uploads a file to the network and returns an UploadFileResponse
func UploadFile(req protocol.UploadFileRequest) protocol.UploadFileResponse {
	fmt.Println("Daemon: handling upload for", req.Path)

	filename := removePath(req.Path)

	// Get the original file's size before writing the stub.
	originalSize := 0
	if info, err := os.Stat(req.Path); err == nil {
		originalSize = int(info.Size())
	}

	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	nodeID := helpers.GetNodeID()
	realPath := filepath.Join(mosaicDir, filename)

	// TODO: distribute file shards to peers here.

	// Update the manifest entry with the latest size.
	if err := filesystem.AddToManifest(mosaicDir, filename, originalSize, nodeID); err != nil {
		fmt.Println("Warning: could not update manifest for", filename, "-", err)
	}

	alreadyCached := false
	if _, err := os.Stat(realPath); err == nil {
		alreadyCached = true
	}

	if alreadyCached {
		// File is cached locally — re-fetch from network to get the updated version.
		fmt.Println("Daemon: re-fetching", filename, "after upload to update local cache")
		fetchResp := DownloadFile(protocol.DownloadFileRequest{FilePath: filename})
		if !fetchResp.Success {
			fmt.Println("Warning: re-fetch after upload failed for", filename, "-", fetchResp.Details)
		}
	} else {
		// File not cached — write a stub so Finder shows the remote-only placeholder.
		if err := filesystem.WriteStub(mosaicDir, filename, originalSize, nodeID); err != nil {
			fmt.Println("Warning: could not write stub for", filename, "-", err)
		}
	}

	return protocol.UploadFileResponse{
		Success:          true,
		Details:          "Upload processed by daemon",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}

func removePath(path string) string {
	return filepath.Base(path)
}
