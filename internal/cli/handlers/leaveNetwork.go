package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Leaves the network and returns a LeaveNetworkResponse
func LeaveNetwork(req protocol.LeaveNetworkRequest) protocol.LeaveNetworkResponse {
	fmt.Println("Daemon: leaving network.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.LeaveNetworkResponse{
		Success:  true,
		Details:  "Network left successfully.",
		Username: helpers.GetUsername(),
	}
}
