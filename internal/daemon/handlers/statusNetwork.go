package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Checks the network status and returns a NetworkStatusResponse
func StatusNetwork(req protocol.NetworkStatusRequest) protocol.NetworkStatusResponse {
	fmt.Println("Daemon: checking status of network.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.NetworkStatusResponse{
		Success:          true,
		Details:          "Network status processed by daemon",
		NetworkStorage:   helpers.NetworkStorage(),
		AvailableStorage: helpers.AvailableStorage(),
		StorageUsed:      helpers.UserStorageUsed(),
		Peers:            helpers.NumPeers(),
	}
}
