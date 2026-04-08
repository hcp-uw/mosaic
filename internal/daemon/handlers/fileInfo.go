package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// Returns file info and returns a FileInfoResponse
func GetFileInfo(req protocol.FileInfoRequest) protocol.FileInfoResponse {
	fmt.Println("Daemon: getting file info.")

	filename := removePath(req.FilePath)
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	// Read size and metadata from the stub written at upload time.
	stub, err := filesystem.ReadStub(mosaicDir, filename)
	size := 0
	dateAdded := ""
	if err == nil {
		size = stub.Size
		dateAdded = stub.DateAdded
	}

	return protocol.FileInfoResponse{
		Success:   true,
		Details:   "File info retrieved successfully.",
		FileName:  filename,
		Username:  helpers.GetUsername(),
		NodeID:    helpers.GetNodeID(),
		DateAdded: dateAdded,
		Size:      size,
	}
}
