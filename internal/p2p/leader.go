package p2p

import (
	"net"

	"github.com/hcp-uw/mosaic/internal/api"
)

func (c *Client) leaderHandleJoiner(joiner *PeerInfo) {
	c.mutex.RLock()
	currentMembers := make(map[string]*net.UDPAddr, len(c.peers))
	for id, info := range c.peers {
		if info != nil && info.ID != joiner.ID {
			currentMembers[id] = info.Address
		}
	}
	id := c.id
	c.mutex.RUnlock()

	currentMembersMsg := api.NewCurrentMembersMessage(currentMembers, id)
	newJoinerMsg := api.NewNewPeerJoinerMessage(id, joiner.ID, joiner.Address.String())

	_ = c.SendToAllPeers(newJoinerMsg)
	_ = c.SendToPeer(joiner.ID, currentMembersMsg)
}
