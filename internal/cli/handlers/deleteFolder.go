package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Deletes a file from the network and returns an DeleteFolderResponse
func DeleteFolder(req protocol.DeleteFolderRequest) protocol.DeleteFolderResponse {
	fmt.Println("Daemon: handling delete for", req.FolderName)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.DeleteFolderResponse{
		Success:          true,
		Details:          "Delete processed by daemon",
		FolderName:       removePath(req.FolderName), // remove path code in upload.go
		AvailableStorage: helpers.AvailableStorage(),
	}
}
