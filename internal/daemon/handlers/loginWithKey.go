package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// LoginKey authenticates the user with their key and persists it so that the
// ECDSA keypair can be deterministically re-derived on any machine.
func LoginKey(req protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	fmt.Println("Daemon: logging in with key.")

	if req.Key == "" {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: "login key must not be empty",
		}
	}

	if err := helpers.SaveLoginKey(req.Key); err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("could not save login key: %v", err),
		}
	}

	return protocol.LoginKeyResponse{
		Success:     true,
		Details:     "Logged in with key successfully.",
		CurrentNode: helpers.GetNodeID(),
		Username:    helpers.GetUsername(),
	}
}
