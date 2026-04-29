package p2p

import (
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// PeerInfo holds information about the assigned peer
type PeerInfo struct {
	Address      *net.UDPAddr
	Conn         *net.UDPConn
	ID           string
	LastPeerPong time.Time
}

// SendToPeer sends data to the connected peer.
//
// Conn and Address are snapshotted under the read lock so the send path
// is safe against concurrent ConnectToPeer mutations. peerId may be
// either an IP:port key or an ed25519 pubkey hex — the latter is
// resolved via the pubkeyToPeerID mapping before the peers lookup.
func (c *Client) SendToPeer(peerId string, message *api.Message) error {
	// Resolve pubkey hex → IP:port if needed (dataOps.mu released before c.mutex).
	c.dataOps.mu.Lock()
	if resolved, ok := c.dataOps.pubkeyToPeerID[peerId]; ok {
		peerId = resolved
	}
	c.dataOps.mu.Unlock()

	c.mutex.RLock()
	peerInfo := c.peers[peerId]
	state := c.state
	var conn *net.UDPConn
	var addr *net.UDPAddr
	if peerInfo != nil {
		conn = peerInfo.Conn
		addr = peerInfo.Address
	}
	c.mutex.RUnlock()

	if peerInfo == nil {
		return fmt.Errorf("no peer information available")
	}
	if conn == nil {
		return fmt.Errorf("not connected to peer")
	}
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}

	data, err := message.Serialize()
	if err != nil {
		return err
	}
	return chunkAndSend(func(b []byte) error {
		_, werr := conn.WriteToUDP(b, addr)
		return werr
	}, c.id, data)
}

// peerSnapshot is a (Conn, Address) pair captured under the lock for safe
// concurrent dispatch.
type peerSnapshot struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

func (c *Client) SendToAllPeers(message *api.Message) error {
	c.mutex.RLock()
	state := c.state
	snaps := make([]peerSnapshot, 0, len(c.peers))
	for _, p := range c.peers {
		if p != nil && p.Conn != nil {
			snaps = append(snaps, peerSnapshot{conn: p.Conn, addr: p.Address})
		}
	}
	c.mutex.RUnlock()

	if len(snaps) == 0 {
		return fmt.Errorf("no peer information available")
	}
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}

	data, err := message.Serialize()
	if err != nil {
		return err
	}
	for _, s := range snaps {
		s := s
		if err := chunkAndSend(func(b []byte) error {
			_, werr := s.conn.WriteToUDP(b, s.addr)
			return werr
		}, c.id, data); err != nil {
			return err
		}
	}
	return nil
}

// IsPeerCommunicationAvailable returns true if peer communication is possible
func (c *Client) IsPeerCommunicationAvailable() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return len(c.GetConnectedPeers()) > 0 && c.state != StateDisconnected
}

// ConnectToPeer attempts to establish direct connection to assigned peer using UDP hole punching
func (c *Client) ConnectToPeer(peer *PeerInfo) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if peer == nil {
		return fmt.Errorf("no peer assigned")
	}

	if c.serverConn == nil {
		return fmt.Errorf("not connected to server")
	}

	// Reuse the existing server connection socket for peer communication
	// This is the key to proper UDP hole punching
	c.peers[peer.ID] = peer
	c.peers[peer.ID].Conn = c.serverConn
	c.peers[peer.ID].LastPeerPong = time.Now() // Initialize peer connection time
	if c.state != StateLeader {
		c.setStateLocked(StateConnectedToPeer)
	}

	// Start UDP hole punching - send initial packet to peer to establish connection
	go c.establishPeerConnection(c.peers[peer.ID].Address)

	return nil
}

// establishPeerConnection performs UDP hole punching to establish peer connection
func (c *Client) establishPeerConnection(peerAddr *net.UDPAddr) {
	c.mutex.RLock()
	peerConn := c.GetPeerById(peerAddr.String()).Conn
	c.mutex.RUnlock()

	if peerConn == nil {
		return
	}

	// Send initial "punch" packets to establish connection
	punchMessage := []byte("STUN_PUNCH")
	for range 3 {
		_, err := peerConn.WriteToUDP(punchMessage, peerAddr)
		if err != nil {
			c.notifyError(fmt.Errorf("failed to send punch packet: %w", err))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
