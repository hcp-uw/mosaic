package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// uploads a folder to the network and returns an UploadFolderResponse
func UploadFolder(req protocol.UploadFolderRequest) protocol.UploadFolderResponse {
	fmt.Println("Daemon: handling upload for", req.FolderPath)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)

	return protocol.UploadFolderResponse{
		Success:          true,
		Details:          "Upload processed by daemon",
		FolderName:       removePath(req.FolderPath),
		AvailableStorage: helpers.AvailableStorage(),
	}
}
