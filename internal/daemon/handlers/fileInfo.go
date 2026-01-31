package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Returns file info and returns a FileInfoResponse
func GetFileInfo(req protocol.FileInfoRequest) protocol.FileInfoResponse {
	fmt.Println("Daemon: getting file info.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.FileInfoResponse{
		Success:   true,
		Details:   "File info retrieved successfully.",
		FileName:  removePath(req.FilePath),
		Username:  helpers.GetUsername(),
		NodeID:    67,
		DateAdded: "06-07-2025",
		Size:      20,
	}
}
