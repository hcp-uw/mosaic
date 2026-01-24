package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Deletes a file from the network and returns an DeleteFileResponse
func DownloadFile(req protocol.DownloadFileRequest) protocol.DownloadFileResponse {
	fmt.Println("Daemon: handling download for", req.FilePath)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.DownloadFileResponse{
		Success:          true,
		Details:          "Download processed by daemon",
		FileName:         removePath(req.FilePath), // Remove path code in upload.go
		AvailableStorage: helpers.AvailableStorage(),
	}
}
