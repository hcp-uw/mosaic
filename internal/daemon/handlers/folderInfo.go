package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Returns folder metadata. TODO: implement real folder tracking.
func GetFolderInfo(req protocol.FolderInfoRequest) protocol.FolderInfoResponse {
	fmt.Println("Daemon: getting folder info for", req.FolderName)
	return protocol.FolderInfoResponse{
		Success:    true,
		Details:    "Folder info retrieved successfully.",
		FolderName: removePath(req.FolderName),
	}
}
