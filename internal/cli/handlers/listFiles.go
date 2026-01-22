package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Lists files and returns a ListFilesResponse
func ListFiles(req protocol.ListFilesRequest) protocol.ListFilesResponse {
	fmt.Println("Daemon: listing files.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)

	files := []string{"text.txt", "pic.jpg"}
	return protocol.ListFilesResponse{
		Success:  true,
		Details:  "Files listed successfully.",
		Username: helpers.GetUsername(),
		// the files should have metadata and stuff but just for concept ill use
		// an array of strings
		Files: files,
	}

}
