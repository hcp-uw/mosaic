package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Logs out of the account and returns a LogoutResponse
func HandleLogout(req protocol.LogoutRequest) protocol.LogoutResponse {
	fmt.Println("Daemon: logging out.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.LogoutResponse{
		Success:  true,
		Details:  "Logged out successfully.",
		Username: helpers.GetUsername(),
	}
}
