package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Lists files by scanning stubs in ~/Mosaic/.
func ListFiles(req protocol.ListFilesRequest) protocol.ListFilesResponse {
	fmt.Println("Daemon: listing files.")

	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	entries, err := os.ReadDir(mosaicDir)
	if err != nil {
		return protocol.ListFilesResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not read Mosaic directory: %v", err),
			Username: helpers.GetUsername(),
			Files:    []string{},
		}
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".mosaic") {
			// Strip .mosaic suffix to get the original filename
			files = append(files, strings.TrimSuffix(entry.Name(), ".mosaic"))
		}
	}

	return protocol.ListFilesResponse{
		Success:  true,
		Details:  "Files listed successfully.",
		Username: helpers.GetUsername(),
		Files:    files,
	}
}
