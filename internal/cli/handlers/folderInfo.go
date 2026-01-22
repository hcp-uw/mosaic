package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Returns file info and returns a FileInfoResponse
func GetFolderInfo(req protocol.FolderInfoRequest) protocol.FolderInfoResponse {
	fmt.Println("Daemon: getting folder info.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.FolderInfoResponse{
		Success:       true,
		Details:       "Folder info retrieved successfully.",
		FolderName:    removePath(req.FolderName),
		NodeID:        41,
		DateAdded:     "07-06-2025",
		Size:          20,
		NumberOfFiles: 5,
	}
}
