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
	Conn         net.PacketConn // direct *net.UDPConn or a TURN relay PacketConn
	ID           string
	IsLeader     bool      // true when this peer is the network leader
	LastPeerPong time.Time
	ViaTURN      bool      // true when Conn routes through the TURN relay
}

// SendToPeer sends data to the connected peer
func (c *Client) SendToPeer(peerId string, message *api.Message) error {
	c.mutex.RLock()
	peerInfo := c.GetPeerById(peerId)
	state := c.state
	c.mutex.RUnlock()

	if peerInfo == nil {
		return fmt.Errorf("no peer information available")
	}

	if peerInfo.Conn == nil {
		return fmt.Errorf("not connected to peer")
	}

	// Block sending only in truly disconnected state
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}

	data, err := message.Serialize()
	if err != nil {
		return err
	}

	_, err = peerInfo.Conn.WriteTo(data, peerInfo.Address)
	return err
}

func (c *Client) SendToAllPeers(message *api.Message) error {
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

	data, err := message.Serialize()
	if err != nil {
		return err
	}

	for _, peer := range allPeers {
		_, err := peer.Conn.WriteTo(data, peer.Address)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendRawToPeer sends raw bytes to a single peer by ID without JSON serialization.
// Used to redistribute encrypted shard frames to a specific peer.
func (c *Client) SendRawToPeer(peerID string, data []byte) error {
	c.mutex.RLock()
	peer := c.GetPeerById(peerID)
	state := c.state
	c.mutex.RUnlock()

	if peer == nil || peer.Conn == nil {
		return fmt.Errorf("peer %s not connected", peerID)
	}
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}
	_, err := peer.Conn.WriteTo(data, peer.Address)
	return err
}

// SendRawToAllPeers sends raw bytes directly to all connected peers without
// any JSON serialization. Used for binary shard frames.
func (c *Client) SendRawToAllPeers(data []byte) error {
	c.mutex.RLock()
	allPeers := c.GetConnectedPeers()
	state := c.state
	c.mutex.RUnlock()

	if len(allPeers) == 0 {
		return fmt.Errorf("no peer information available")
	}
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}
	for _, peer := range allPeers {
		if _, err := peer.Conn.WriteTo(data, peer.Address); err != nil {
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
		c.setState(StateConnectedToPeer)
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
		_, err := peerConn.WriteTo(punchMessage, peerAddr)
		if err != nil {
			c.notifyError(fmt.Errorf("failed to send punch packet: %w", err))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
