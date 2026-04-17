package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Logs out of the account and returns a LogoutResponse
func HandleLogout(req protocol.LogoutRequest) protocol.LogoutResponse {
	fmt.Println("Daemon: logging out.")

	username := helpers.GetUsername()

	// Clear all local auth state.
	_ = helpers.ClearSession()
	_ = helpers.ClearLoginKey()

	return protocol.LogoutResponse{
		Success:  true,
		Details:  "Logged out successfully.",
		Username: username,
	}
}
