package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// Returns the list of connected peers. TODO: implement real peer discovery.
func GetPeers(req protocol.GetPeersRequest) protocol.GetPeersResponse {
	fmt.Println("Daemon: getting peers.")
	return protocol.GetPeersResponse{
		Success: true,
		Details: "Peers fetched successfully.",
		Peers:   []protocol.Peer{},
	}
}
