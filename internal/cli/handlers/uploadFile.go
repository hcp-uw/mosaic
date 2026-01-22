package handlers

import (
	"fmt"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// uploads a file to the network and returns an UploadFileResponse
func UploadFile(req protocol.UploadFileRequest) protocol.UploadFileResponse {
	fmt.Println("Daemon: handling upload for", req.Path)
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)

	return protocol.UploadFileResponse{
		Success:          true,
		Details:          "Upload processed by daemon",
		FileName:         removePath(req.Path),
		AvailableStorage: helpers.AvailableStorage(),
	}
}

func removePath(path string) string {
	return filepath.Base(path)
}
