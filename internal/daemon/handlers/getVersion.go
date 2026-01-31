package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Returns file info and returns a FileInfoResponse
func GetVersion(req protocol.VersionRequest) protocol.VersionResponse {
	fmt.Println("Daemon: getting version.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.VersionResponse{
		Success: true,
		Details: "Version info retrieved successfully.",
		Version: "1.2.26",
	}
}
