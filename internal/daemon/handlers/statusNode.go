package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Checks the network status and returns a NetworkStatusResponse
func StatusNode(req protocol.NodeStatusRequest) protocol.NodeStatusResponse {
	fmt.Println("Daemon: checking status of node.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.NodeStatusResponse{
		Success:      true,
		Details:      "Node status processed by daemon.",
		Username:     helpers.GetUsername(),
		ID:           req.ID,
		StorageShare: helpers.StorageShare(),
	}
}
