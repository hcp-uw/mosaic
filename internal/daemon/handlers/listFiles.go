package handlers

import (
	"fmt"
	"os"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Lists files by scanning stubs in ~/Mosaic/.
func ListFiles(req protocol.ListFilesRequest) protocol.ListFilesResponse {
	fmt.Println("Daemon: listing files.")

	mosaicDir := shared.MosaicDir()
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
