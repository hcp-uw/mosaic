package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// Lists files by reading the manifest (source of truth for all network files).
func ListFiles(req protocol.ListFilesRequest) protocol.ListFilesResponse {
	fmt.Println("Daemon: listing files.")

	mosaicDir := shared.MosaicDir()
	entries, err := filesystem.ReadManifest(mosaicDir)
	if err != nil {
		return protocol.ListFilesResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not read manifest: %v", err),
			Username: helpers.GetUsername(),
			Files:    []string{},
		}
	}

	files := make([]string, 0, len(entries))
	for name := range entries {
		files = append(files, name)
	}

	return protocol.ListFilesResponse{
		Success:  true,
		Details:  "Files listed successfully.",
		Username: helpers.GetUsername(),
		Files:    files,
	}
}
