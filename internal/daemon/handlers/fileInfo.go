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

	// Read metadata from the manifest (authoritative) with fallback to stub.
	size := 0
	dateAdded := ""
	entries, err := filesystem.ReadManifest(mosaicDir)
	if err == nil {
		if entry, ok := entries[filename]; ok {
			size = entry.Size
			dateAdded = entry.DateAdded
		}
	} else {
		// Fallback: read from stub (e.g. manifest doesn't exist yet).
		stub, serr := filesystem.ReadStub(mosaicDir, filename)
		if serr == nil {
			size = stub.Size
			dateAdded = stub.DateAdded
		}
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
