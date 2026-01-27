package p2p

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// ClientState represents the current state of the client
type ClientState int

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateWaiting
	StatePaired
	StateConnectedToPeer
)

// String returns string representation of ClientState
func (s ClientState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StateWaiting:
		return "Waiting"
	case StatePaired:
		return "Paired"
	case StateConnectedToPeer:
		return "ConnectedToPeer"
	default:
		return "Unknown"
	}
}

// PeerInfo holds information about the assigned peer
type PeerInfo struct {
	Address *net.UDPAddr
	ID      string
}

// Client represents a STUN client
type Client struct {
	serverAddr       *net.UDPAddr
	conn             *net.UDPConn
	state            ClientState
	peerInfo         *PeerInfo
	peerConn         *net.UDPConn
	lastPeerPong     time.Time
	mutex            sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	stateCallbacks   []func(ClientState)
	peerCallbacks    []func(*PeerInfo)
	errorCallbacks   []func(error)
	messageCallbacks []func([]byte)
}

// ClientConfig holds client configuration
type ClientConfig struct {
	ServerAddress  string
	PingInterval   time.Duration
	ConnectTimeout time.Duration
}

// DefaultClientConfig returns default client configuration
func DefaultClientConfig(serverAddr string) *ClientConfig {
	return &ClientConfig{
		ServerAddress:  serverAddr,
		PingInterval:   10 * time.Second,
		ConnectTimeout: 30 * time.Second,
	}
}

// NewClient creates a new Node client
func NewClient(config *ClientConfig) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	serverAddr, err := net.ResolveUDPAddr("udp", config.ServerAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve server address: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		serverAddr:       serverAddr,
		state:            StateDisconnected,
		ctx:              ctx,
		cancel:           cancel,
		stateCallbacks:   make([]func(ClientState), 0),
		peerCallbacks:    make([]func(*PeerInfo), 0),
		errorCallbacks:   make([]func(error), 0),
		messageCallbacks: make([]func([]byte), 0),
	}, nil
}

// Connect establishes connection to STUN server
func (c *Client) Connect() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.state != StateDisconnected {
		return fmt.Errorf("client already connected or connecting")
	}

	// Use ListenUDP to create an unconnected socket that can send to multiple addresses
	localAddr, err := net.ResolveUDPAddr("udp", ":0") // Use random local port
	if err != nil {
		return fmt.Errorf("failed to resolve local address: %w", err)
	}

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return fmt.Errorf("failed to create UDP socket: %w", err)
	}

	c.conn = conn
	c.setState(StateConnecting)

	// Start message handling
	go c.handleMessages()

	// Start ping routine
	go c.pingRoutine()

	// Register with server
	return c.register()
}

// Disconnect closes the connection to the server
func (c *Client) Disconnect() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cancel()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// Note: peerConn is the same as conn, so don't close it twice
	c.peerConn = nil

	c.setState(StateDisconnected)
	c.peerInfo = nil

	return nil
}

// ConnectToPeer attempts to establish direct connection to assigned peer using UDP hole punching
func (c *Client) ConnectToPeer() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.peerInfo == nil {
		return fmt.Errorf("no peer assigned")
	}

	if c.conn == nil {
		return fmt.Errorf("not connected to server")
	}

	// Reuse the existing server connection socket for peer communication
	// This is the key to proper UDP hole punching
	c.peerConn = c.conn
	c.lastPeerPong = time.Now() // Initialize peer connection time
	c.setState(StateConnectedToPeer)

	// Start UDP hole punching - send initial packet to peer to establish connection
	go c.establishPeerConnection(c.peerInfo.Address)

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

// SendToPeer sends data to the connected peer
func (c *Client) SendToPeer(data []byte) error {
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

// GetState returns current client state
func (c *Client) GetState() ClientState {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.state
}

// GetPeerInfo returns information about assigned peer
func (c *Client) GetPeerInfo() *PeerInfo {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.peerInfo
}

// IsPeerCommunicationAvailable returns true if peer communication is possible
func (c *Client) IsPeerCommunicationAvailable() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.peerInfo != nil && c.peerConn != nil && c.state != StateDisconnected
}

// register sends registration message to server
func (c *Client) register() error {
	msg := api.NewClientRegisterMessage()
	return c.sendToServer(msg)
}

// sendToServer sends a message to the STUN server
func (c *Client) sendToServer(msg *api.Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	_, err = c.conn.WriteToUDP(data, c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to send message to server: %w", err)
	}

	return nil
}

// sendPeerPing sends a ping message to the connected peer
func (c *Client) sendPeerPing() error {
	c.mutex.RLock()
	peerConn := c.peerConn
	peerInfo := c.peerInfo
	c.mutex.RUnlock()

	if peerConn == nil {
		return fmt.Errorf("not connected to peer")
	}

	if peerInfo == nil {
		return fmt.Errorf("no peer information available")
	}

	msg := api.NewPeerPingMessage()
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize peer ping: %w", err)
	}

	_, err = peerConn.WriteToUDP(data, peerInfo.Address)
	if err != nil {
		return fmt.Errorf("failed to send peer ping: %w", err)
	}

	return nil
}

// sendPeerPong sends a pong response to the connected peer
func (c *Client) sendPeerPong() error {
	c.mutex.RLock()
	peerConn := c.peerConn
	peerInfo := c.peerInfo
	c.mutex.RUnlock()

	if peerConn == nil {
		return fmt.Errorf("not connected to peer")
	}

	if peerInfo == nil {
		return fmt.Errorf("no peer information available")
	}

	msg := api.NewPeerPongMessage()
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize peer pong: %w", err)
	}

	_, err = peerConn.WriteToUDP(data, peerInfo.Address)
	if err != nil {
		return fmt.Errorf("failed to send peer pong: %w", err)
	}

	return nil
}

// handleMessages processes incoming messages and routes them between server and peer
func (c *Client) handleMessages() {
	buffer := make([]byte, 1024)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, fromAddr, err := c.conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			c.notifyError(fmt.Errorf("failed to read from connection: %w", err))
			continue
		}

		// Route message based on sender address
		if fromAddr.String() == c.serverAddr.String() {
			// Message from server - process as server message
			c.processServerMessage(buffer[:n])
		} else {
			// Message from peer - route to peer message channel
			c.processPeerMessage(buffer[:n])
		}
	}
}

// processServerMessage processes a message from the server
func (c *Client) processServerMessage(data []byte) {
	msg, err := api.DeserializeMessage(data)
	if err != nil {
		c.notifyError(fmt.Errorf("failed to deserialize server message: %w", err))
		return
	}

	c.processMessage(msg)
}

// processPeerMessage processes a message from a peer
func (c *Client) processPeerMessage(data []byte) {
	// Filter out STUN punch packets
	if string(data) == "STUN_PUNCH" {
		return // Ignore punch packets
	}

	// Try to parse as a STUN message first (for ping/pong)
	if msg, err := api.DeserializeMessage(data); err == nil {
		switch msg.Type {
		case api.PeerPing:
			// Respond with pong
			c.sendPeerPong()
			return
		case api.PeerPong:
			// Update last pong time
			c.mutex.Lock()
			c.lastPeerPong = time.Now()
			c.mutex.Unlock()
			return
		}
		// If it's another type of STUN message, don't add to channel
		return
	}

	// Notify message received callbacks
	c.notifyMessageReceived(data)
}

// processMessage processes a message from the server
func (c *Client) processMessage(msg *api.Message) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	switch msg.Type {
	case api.WaitingForPeer:
		c.setState(StateWaiting)

	case api.PeerAssignment:
		data, err := msg.GetPeerAssignmentData()
		if err != nil {
			c.notifyError(fmt.Errorf("failed to parse peer assignment: %w", err))
			return
		}

		peerAddr, err := net.ResolveUDPAddr("udp", data.PeerAddress)
		if err != nil {
			c.notifyError(fmt.Errorf("failed to resolve peer address: %w", err))
			return
		}

		c.peerInfo = &PeerInfo{
			Address: peerAddr,
			ID:      data.PeerID,
		}

		c.setState(StatePaired)
		c.notifyPeerAssigned(c.peerInfo)

	case api.ServerError:
		data, err := msg.GetServerErrorData()
		if err != nil {
			c.notifyError(fmt.Errorf("failed to parse server error: %w", err))
			return
		}

		c.notifyError(fmt.Errorf("server error [%s]: %s", data.ErrorCode, data.ErrorMessage))

	default:
		c.notifyError(fmt.Errorf("unknown message type: %s", msg.Type))
	}
}

// pingRoutine sends periodic ping messages to keep connection alive
func (c *Client) pingRoutine() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mutex.RLock()
			state := c.state
			peerInfo := c.peerInfo
			c.mutex.RUnlock()

			if state == StateDisconnected {
				return
			}

			// Send server pings only when connecting/waiting (stop after peer connection)
			if state == StateConnecting || state == StateWaiting {
				msg := api.NewClientPingMessage()
				if err := c.sendToServer(msg); err != nil {
					c.notifyError(fmt.Errorf("failed to send server ping: %w", err))
				}
			}

			// Send peer pings when connected to peer
			if state == StateConnectedToPeer && peerInfo != nil {
				// Check for peer timeout (30 seconds without pong)
				c.mutex.RLock()
				lastPong := c.lastPeerPong
				c.mutex.RUnlock()

				if time.Since(lastPong) > 30*time.Second {
					// Clear peer info and re-register with server
					c.mutex.Lock()
					c.peerInfo = nil
					c.peerConn = nil
					c.lastPeerPong = time.Time{}
					c.setState(StateWaiting)
					c.mutex.Unlock()

					// Re-register with server since we were removed when paired
					if err := c.register(); err != nil {
						c.notifyError(fmt.Errorf("failed to re-register after peer timeout: %w", err))
					} else {
						c.notifyError(fmt.Errorf("peer connection timeout - re-registered with server"))
					}
					continue
				}

				if err := c.sendPeerPing(); err != nil {
					c.notifyError(fmt.Errorf("failed to send peer ping: %w", err))
				}
			}
		}
	}
}
