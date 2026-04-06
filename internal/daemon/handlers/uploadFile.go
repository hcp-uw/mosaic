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
	// all the actual logic and stuff goes here

	filename := removePath(req.Path)

	// Write a stub file to ~/Mosaic/ so Finder shows the file with a badge.
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	if err := filesystem.WriteStub(mosaicDir, filename, 0, helpers.GetNodeID()); err != nil {
		fmt.Println("Warning: could not write stub for", filename, "-", err)
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
