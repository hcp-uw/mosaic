package handlers

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

// GetPeers returns information about every peer currently connected to this node.
func GetPeers(req protocol.GetPeersRequest) protocol.GetPeersResponse {
	fmt.Println("Daemon: getting peers.")

	client := GetP2PClient()
	if client == nil {
		return protocol.GetPeersResponse{
			Success: false,
			Details: "Not connected to network — run 'mos join network' first.",
			Peers:   []protocol.Peer{},
		}
	}

	connected := client.GetConnectedPeers()
	peers := make([]protocol.Peer, 0, len(connected))
	for _, p := range connected {
		connType := "direct"
		if p.ViaTURN {
			connType = "relay"
		}
		peers = append(peers, protocol.Peer{
			Username:      fmt.Sprintf("%s (%s)", p.ID[:8], connType),
			NodeID:        deriveNodeIDFromPeerID(p.ID),
			StorageShared: helpers.StorageShare(),
		})
	}

	details := fmt.Sprintf("%d peer(s) connected.", len(peers))
	return protocol.GetPeersResponse{
		Success: true,
		Details: details,
		Peers:   peers,
	}
}

// deriveNodeIDFromPeerID produces a stable integer node ID from the P2P peer ID string.
func deriveNodeIDFromPeerID(peerID string) int {
	h := sha256.Sum256([]byte(peerID))
	return int(binary.BigEndian.Uint32(h[:4]))
}
