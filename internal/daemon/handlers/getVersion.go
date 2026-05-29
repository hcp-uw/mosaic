package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/version"
)

func GetVersion(req protocol.VersionRequest) protocol.VersionResponse {
	fmt.Println("Daemon: getting version.")
	return protocol.VersionResponse{
		Success: true,
		Details: "Version info retrieved successfully.",
		Version: version.Version,
	}
}
