package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Gets account status and returns a StatusAccountResponse
func StatusAccount(req protocol.StatusAccountRequest) protocol.StatusAccountResponse {
	fmt.Println("Daemon: getting account status.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.StatusAccountResponse{
		Success:          true,
		Details:          "Network joined successfully.",
		Nodes:            []string{"node-1", "node-2", "node-3"}, // this would probably not be strings but i did it as a placeholder temporarily
		GivenStorage:     helpers.AccountGivenStorage(),
		AvailableStorage: helpers.AvailableStorage(),
		UsedStorage:      helpers.UserStorageUsed(),
		Username:         helpers.GetUsername(),
	}
}
