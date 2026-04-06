package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// Deletes a file from the network and returns an DeleteFileResponse
func DeleteFile(req protocol.DeleteFileRequest) protocol.DeleteFileResponse {
	fmt.Println("Daemon: handling delete for", req.FilePath)
	// all the actual logic and stuff goes here

	filename := removePath(req.FilePath)

	// Remove the stub from ~/Mosaic/ so Finder stops showing this file.
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	if err := filesystem.RemoveStub(mosaicDir, filename); err != nil {
		fmt.Println("Warning: could not remove stub for", filename, "-", err)
	}

	return protocol.DeleteFileResponse{
		Success:          true,
		Details:          "Delete processed by daemon",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
