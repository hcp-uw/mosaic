package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Deletes a file from the network and returns an DeleteFileResponse
func DeleteFile(req protocol.DeleteFileRequest) protocol.DeleteFileResponse {
	fmt.Println("Daemon: handling delete for", req.FilePath)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.DeleteFileResponse{
		Success:          true,
		Details:          "Delete processed by daemon",
		FileName:         removePath(req.FilePath), // remove path code in upload.go
		AvailableStorage: helpers.AvailableStorage(),
	}
}
