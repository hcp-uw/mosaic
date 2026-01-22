package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Joins the network and returns a JoinResponse
func LoginKey(req protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	fmt.Println("Daemon: logging in with key.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.LoginKeyResponse{
		Success:     true,
		Details:     "Logged in with key successfully.",
		CurrentNode: helpers.GetNodeID(),
		Username:    helpers.GetUsername(),
	}
}
