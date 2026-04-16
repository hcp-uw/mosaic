package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
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

	// Verify content integrity against the manifest hash (if one was recorded at upload time).
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")
	if entry, err := filesystem.GetManifestEntry(mosaicDir, filename); err == nil && entry.ContentHash != "" {
		actualHash, herr := sha256File(destPath)
		if herr != nil || actualHash != entry.ContentHash {
			os.Remove(destPath)
			fmt.Println("Daemon: integrity check failed for", filename, "— file removed")
			return protocol.DownloadFileResponse{
				Success:          false,
				Details:          "file integrity check failed: content hash mismatch",
				FileName:         filename,
				AvailableStorage: helpers.AvailableStorage(),
			}
		}
		fmt.Println("Daemon: integrity verified for", filename)
	}

	return protocol.DownloadFileResponse{
		Success:          true,
		Details:          "file written to disk",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
