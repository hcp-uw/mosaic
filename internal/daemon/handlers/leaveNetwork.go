package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

func LeaveNetwork(req protocol.LeaveNetworkRequest) protocol.LeaveNetworkResponse {
	fmt.Println("Daemon: leaving network.")

	client := GetP2PClient()
	if client == nil {
		return protocol.LeaveNetworkResponse{
			Success:  false,
			Details:  "not currently connected to a network",
			Username: helpers.GetUsername(),
		}
	}

	if err := client.DisconnectFromStun(); err != nil {
		return protocol.LeaveNetworkResponse{
			Success:  false,
			Details:  fmt.Sprintf("failed to disconnect: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	SetP2PClient(nil)

	return protocol.LeaveNetworkResponse{
		Success:  true,
		Details:  "Left network successfully.",
		Username: helpers.GetUsername(),
	}
}
