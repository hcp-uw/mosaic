package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// DownloadFile retrieves a file's bytes from the peer network via FetchFileBytes,
// writes them to ~/Mosaic/<filename>, then returns a response the caller can use
// to open the file with the correct app.
func DownloadFile(req protocol.DownloadFileRequest) protocol.DownloadFileResponse {
	filename := removePath(req.FilePath)
	fmt.Println("Daemon: fetching", filename, "from network")

	data, err := FetchFileBytes(filename)
	if err != nil {
		fmt.Println("Daemon: fetch failed for", filename, "-", err)
		return protocol.DownloadFileResponse{
			Success:          false,
			Details:          fmt.Sprintf("fetch failed: %v", err),
			FileName:         filename,
			AvailableStorage: helpers.AvailableStorage(),
		}
	}

	destPath := filepath.Join(os.Getenv("HOME"), "Mosaic", filename)
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		fmt.Println("Daemon: could not write", destPath, "-", err)
		return protocol.DownloadFileResponse{
			Success:          false,
			Details:          fmt.Sprintf("write failed: %v", err),
			FileName:         filename,
			AvailableStorage: helpers.AvailableStorage(),
		}
	}

	fmt.Println("Daemon: wrote", len(data), "bytes to", destPath)
	return protocol.DownloadFileResponse{
		Success:          true,
		Details:          "file written to disk",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
