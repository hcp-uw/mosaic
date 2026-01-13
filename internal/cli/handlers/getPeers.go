package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Joins the network and returns a JoinResponse
func GetPeers(req protocol.GetPeersRequest) protocol.GetPeersResponse {
	fmt.Println("Daemon: getting peers.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)

	peers := []protocol.Peer{
		{Username: "Gavin", NodeID: 67, StorageShared: 15},
		{Username: "Vihan", NodeID: 68, StorageShared: 20},
	}
	return protocol.GetPeersResponse{
		Success: true,
		Details: "Peers fetched successfully.",
		Peers:   peers,
	}

}
