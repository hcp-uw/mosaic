package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// Lists files by reading the manifest (source of truth for all network files).
func ListFiles(_ protocol.ListFilesRequest) protocol.ListFilesResponse {
	fmt.Println("Daemon: listing files.")

	mosaicDir := shared.MosaicDir()
	entries, err := filesystem.ReadManifest(mosaicDir)
	if err != nil {
		return protocol.ListFilesResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not read manifest: %v", err),
			Username: helpers.GetUsername(),
			Files:    []protocol.LocalFileEntry{},
		}
	}

	files := make([]protocol.LocalFileEntry, 0, len(entries))
	for _, entry := range entries {
		realPath := filepath.Join(mosaicDir, entry.Name)
		stubPath := filepath.Join(mosaicDir, entry.Name+".mosaic")
		_, realErr := os.Stat(realPath)
		_, stubErr := os.Stat(stubPath)
		if realErr != nil && stubErr != nil {
			continue // no physical file on disk — skip
		}
		files = append(files, protocol.LocalFileEntry{
			Name:   entry.Name,
			Cached: entry.Cached,
		})
	}

	return protocol.ListFilesResponse{
		Success:  true,
		Details:  "Files listed successfully.",
		Username: helpers.GetUsername(),
		Files:    files,
	}
}
