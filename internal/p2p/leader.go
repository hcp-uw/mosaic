package p2p

import (
	"net"

	"github.com/hcp-uw/mosaic/internal/api"
)

func (c *Client) leaderHandleJoiner(joiner *PeerInfo) {

	currentMembers := make(map[string]*net.UDPAddr)

	for id, info := range c.peers {
		if info.ID != joiner.ID {
			currentMembers[id] = info.Address
		}
	}

	currentMembersMsg := api.NewCurrentMembersMessage(currentMembers, c.id)
	newJoinerMsg := api.NewNewPeerJoinerMessage(c.id, joiner.ID, joiner.Address.String())

	c.SendToAllPeers(newJoinerMsg)
	c.SendToPeer(joiner.ID, currentMembersMsg)

}
