package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Sets the storage shared by the node and returns a SetStorageResponse
func SetStorage(req protocol.SetStorageRequest) protocol.SetStorageResponse {
	fmt.Println("Daemon: setting account storage.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.SetStorageResponse{
		Success:          true,
		Details:          "Storage set successfully.",
		CurrentNode:      req.Node,
		NodeStorage:      req.Amount,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
