package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

func StatusNetwork(req protocol.NetworkStatusRequest) protocol.NetworkStatusResponse {
	fmt.Println("Daemon: checking status of network.")

	resp := protocol.NetworkStatusResponse{
		Success:          true,
		NetworkStorage:   helpers.NetworkStorage(),
		AvailableStorage: helpers.AvailableStorage(),
		StorageUsed:      helpers.UserStorageUsed(),
	}

	client := GetP2PClient()
	if client == nil {
		resp.Connected = false
		resp.State = "Disconnected"
		return resp
	}

	state := client.GetState()
	peers := client.GetConnectedPeers()

	resp.Connected = state != p2p.StateDisconnected
	resp.State = state.String()
	resp.IsLeader = state == p2p.StateLeader
	resp.Peers = len(peers)

	addrs := make([]string, 0, len(peers))
	for _, peer := range peers {
		if peer.Address != nil {
			addrs = append(addrs, peer.Address.String())
		}
	}
	resp.PeerAddresses = addrs

	return resp
}
