package p2p

import (
	"net"

	"github.com/hcp-uw/mosaic/internal/api"
)

func (c *Client) leaderHandleJoiner(joiner *PeerInfo) {
	currentMembers := make(map[string]*net.UDPAddr)

	c.mutex.RLock()
	for id, info := range c.peers {
		if info.ID != joiner.ID {
			currentMembers[id] = info.Address
		}
	}
	c.mutex.RUnlock()

	currentMembersMsg := api.NewCurrentMembersMessage(currentMembers, c.id)
	newJoinerMsg := api.NewNewPeerJoinerMessage(c.id, joiner.ID, joiner.Address.String())

	// Notify only existing members (not the joiner itself) about the new arrival.
	// Sending NewPeerJoiner back to the joiner would cause it to add itself as
	// a peer, breaking shard routing and triggering phantom redistribution.
	c.mutex.RLock()
	for id, peer := range c.peers {
		if id == joiner.ID || peer.ID == joiner.ID || peer.Conn == nil {
			continue
		}
		c.mutex.RUnlock()
		c.writeToPeer(peer, newJoinerMsg) //nolint:errcheck — best-effort
		c.mutex.RLock()
	}
	c.mutex.RUnlock()

	c.SendToPeer(joiner.ID, currentMembersMsg) //nolint:errcheck — best-effort
}
