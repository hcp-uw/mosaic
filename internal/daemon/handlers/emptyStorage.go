package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// Deletes all user data and returns a EmptyStorageResponse
func EmptyStorage(req protocol.EmptyStorageRequest) protocol.EmptyStorageResponse {
	fmt.Println("Daemon: deleting all storage.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)
	return protocol.EmptyStorageResponse{
		Success:          true,
		Details:          "Storage deleted successfully.",
		StorageDeleted:   helpers.UserStorageUsed(),
		AvailableStorage: helpers.AvailableStorage(),
		Username:         helpers.GetUsername(),
	}
}
