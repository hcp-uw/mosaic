package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Sets the storage shared by the node and returns a SetStorageResponse
func SetStorage(req protocol.SetStorageRequest) protocol.SetStorageResponse {
	fmt.Println("Daemon: setting account storage.")
	return protocol.SetStorageResponse{
		Success:          true,
		Details:          "Storage set successfully.",
		CurrentNode:      req.Node,
		NodeStorage:      req.Amount,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
