package handlers

import (
	"fmt"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// uploads a file to the network and returns an UploadResponse
func HandleUpload(req protocol.UploadRequest) protocol.UploadResponse {
	fmt.Println("Daemon: handling upload for", req.Path)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	fileInfo, _ := os.Stat(req.Path)
	return protocol.UploadResponse{
		Success:          true,
		Details:          "Upload processed by daemon",
		Name:             fileInfo.Name(),
		AvailableStorage: helpers.AvailableStorage(),
	}
}
