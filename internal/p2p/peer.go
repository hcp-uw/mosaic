package p2p

import (
	"fmt"
	"net"
	"time"
)

// PeerInfo holds information about the assigned peer
type PeerInfo struct {
	Address *net.UDPAddr
	Conn    *net.UDPConn
	ID      string
}

// SendToPeer sends data to the connected peer
func (c *Client) SendToPeer(peerId string, data []byte) error {
	c.mutex.RLock()
	peerConn := c.peerConn
	peerInfo := c.peerInfo
	state := c.state
	c.mutex.RUnlock()

	if peerInfo == nil {
		return fmt.Errorf("no peer information available")
	}

	if peerConn == nil {
		return fmt.Errorf("not connected to peer")
	}

	// Block sending only in truly disconnected state
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}

	_, err := peerConn.WriteToUDP(data, peerInfo.Address)
	return err
}

func (c *Client) SendToAllPeers(data []byte) error {
	c.mutex.RLock()
	allPeers := c.GetConnectedPeers()
	state := c.state
	c.mutex.RUnlock()

	if len(allPeers) == 0 {
		return fmt.Errorf("no peer information available")
	}

	// Block sending only in truly disconnected state
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}

	for _, peer := range allPeers {
		_, err := peer.Conn.WriteToUDP(data, peer.Address)
		if err != nil {
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

	if c.peerInfo == nil {
		return fmt.Errorf("no peer assigned")
	}

	if c.serverConn == nil {
		return fmt.Errorf("not connected to server")
	}

	// Reuse the existing server connection socket for peer communication
	// This is the key to proper UDP hole punching
	c.peers[peer.ID] = peer
	c.peers[peer.ID].Conn = c.serverConn
	c.lastPeerPong = time.Now() // Initialize peer connection time
	c.setState(StateConnectedToPeer)

	// Start UDP hole punching - send initial packet to peer to establish connection
	go c.establishPeerConnection(c.peers[peer.ID].Address)

	return nil
}

// establishPeerConnection performs UDP hole punching to establish peer connection
func (c *Client) establishPeerConnection(peerAddr *net.UDPAddr) {
	c.mutex.RLock()
	peerConn := c.peerConn
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
