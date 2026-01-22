package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Deletes a file from the network and returns an DownloadFolderResponse
func DownloadFolder(req protocol.DownloadFolderRequest) protocol.DownloadFolderResponse {
	fmt.Println("Daemon: handling download for", req.FolderPath)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.DownloadFolderResponse{
		Success:          true,
		Details:          "Download processed by daemon",
		FolderName:       removePath(req.FolderPath), // Remove path code in upload.go
		AvailableStorage: helpers.AvailableStorage(),
	}
}
