package p2p

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// Client represents a STUN client
type Client struct {
	id            string
	queuePosition int // server-assigned position; 1 = leader, 2 = next, etc.
	serverAddr       *net.UDPAddr
	serverConn       *net.UDPConn
	turnAddr         string // TURN server "host:port", empty = disabled
	turnUsername     string
	turnPassword     string
	state            ClientState
	peers            map[string]*PeerInfo
	mutex            sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	stateCallbacks   []func(ClientState)
	peerCallbacks    []func(*PeerInfo)
	errorCallbacks   []func(error)
	messageCallbacks []func([]byte)

	// STUN reconnect state (leader only)
	stunFailCount    int
	stunReconnecting bool

	// registrationDone is written once by processMessage when RegisterSuccess
	// arrives. ConnectToStun blocks on it so the CLI gets a real confirmation
	// that the STUN server received and acknowledged the registration.
	// Nil after the first registration completes.
	registrationDone chan error
}

// ClientConfig holds client configuration
type ClientConfig struct {
	ServerAddress  string
	TURNAddress    string // optional — empty disables TURN fallback
	TURNUsername   string
	TURNPassword   string
	PingInterval   time.Duration
	ConnectTimeout time.Duration
}

// DefaultClientConfig returns default client configuration with TURN fallback enabled.
func DefaultClientConfig(serverAddr, turnAddr, turnUsername, turnPassword string) *ClientConfig {
	return &ClientConfig{
		ServerAddress:  serverAddr,
		TURNAddress:    turnAddr,
		TURNUsername:   turnUsername,
		TURNPassword:   turnPassword,
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
		turnAddr:         config.TURNAddress,
		turnUsername:     config.TURNUsername,
		turnPassword:     config.TURNPassword,
		state:            StateDisconnected,
		peers:            make(map[string]*PeerInfo),
		ctx:              ctx,
		cancel:           cancel,
		stateCallbacks:   make([]func(ClientState), 0),
		peerCallbacks:    make([]func(*PeerInfo), 0),
		errorCallbacks:   make([]func(error), 0),
		messageCallbacks: make([]func([]byte), 0),
	}, nil
}

// GetState returns current client state
func (c *Client) GetState() ClientState {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.state
}

func (c *Client) GetID() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.id
}

func (c *Client) GetConnectedPeers() []*PeerInfo {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	info := []*PeerInfo{}

	for _, val := range c.peers {
		if val.Conn != nil {
			info = append(info, val)
		}
	}

	return info
}

func (c *Client) GetPeerById(id string) *PeerInfo {
	return c.peers[id]
}

// register sends registration message to server.
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

	_, err = c.serverConn.WriteToUDP(data, c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to send message to server: %w", err)
	}

	return nil
}

// handleMessages processes incoming messages and routes them between server and peer
func (c *Client) handleMessages() {
	buffer := make([]byte, 65507)

	// When the context is cancelled, nudge the blocked ReadFromUDP by setting a
	// deadline of now. This avoids calling SetReadDeadline on every iteration
	// (a syscall per packet that was limiting throughput to ~42K packets/sec).
	go func() {
		<-c.ctx.Done()
		if c.serverConn != nil {
			c.serverConn.SetReadDeadline(time.Now())
		}
	}()

	for {
		n, fromAddr, err := c.serverConn.ReadFromUDP(buffer)
		if err != nil {
			if c.ctx.Err() != nil {
				return // context cancelled — expected shutdown
			}
			c.notifyError(fmt.Errorf("failed to read from connection: %w", err))
			continue
		}

		// Copy bytes before dispatch — buffer is reused on the next ReadFromUDP
		// and callbacks fire in separate goroutines, so without a copy they race.
		msg := make([]byte, n)
		copy(msg, buffer[:n])

		// Route message based on sender address
		if fromAddr.String() == c.serverAddr.String() {
			c.processServerMessage(msg)
		} else {
			c.processPeerMessage(msg)
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

	// Binary shard frames start with 0x01. JSON always starts with '{' (0x7B).
	// Route binary frames directly to the application layer without any parsing.
	if len(data) > 0 && data[0] == 0x01 {
		c.notifyMessageReceived(data)
		return
	}

	// Try to parse as a structured message first.
	if msg, err := api.DeserializeMessage(data); err == nil {
		switch msg.Type {
		case api.PeerPing:
			c.sendPeerPong(msg.Sign.PubKey)
			return
		case api.PeerPong:
			c.mutex.Lock()
			if peer, ok := c.peers[msg.Sign.PubKey]; ok {
				peer.LastPeerPong = time.Now()
			}
			c.mutex.Unlock()
			return
		case api.PeerTextMessage:
			data, err := msg.GetPeerTextMessageData()
			if err != nil {
				c.notifyError(fmt.Errorf("Failed to read PeerTxtMessageData"))
			}
			c.notifyMessageReceived([]byte(data.Message))

		case api.NewPeerJoiner:
			data, err := msg.GetNewPeerJoinerData()
			if err != nil {
				c.notifyError(fmt.Errorf("Failed to parse new joiner data %w", err))
				return
			}

			peerAddr, err := net.ResolveUDPAddr("udp", data.JoinerAddress)
			if err != nil {
				c.notifyError(fmt.Errorf("failed to resolve peer address: %w", err))
				return
			}
			peerInfo := &PeerInfo{
				Address: peerAddr,
				ID:      data.JoinerID,
			}

			c.mutex.Lock()
			c.peers[data.JoinerAddress] = peerInfo
			c.mutex.Unlock()

			c.notifyPeerAssigned(peerInfo)
		case api.CurrentMembers:
			data, err := msg.GetCurrentMembersData()
			if err != nil {
				c.notifyError(fmt.Errorf("failed to parse peer assignment: %w", err))
				return
			}

			for id, addr := range data.Members {
				peerAddr, err := net.ResolveUDPAddr("udp", addr)
				if err != nil {
					c.notifyError(fmt.Errorf("failed to resolve peer address: %w", err))
					return
				}
				peerInfo := &PeerInfo{
					Address: peerAddr,
					ID:      id,
				}

				c.mutex.Lock()
				c.peers[id] = peerInfo
				c.mutex.Unlock()

				c.notifyPeerAssigned(peerInfo)
			}

		case api.ManifestSync:
			// Route manifest sync messages to the application callback layer.
			c.notifyMessageReceived(data)
			return
		case api.ShardPush, api.ShardRequest, api.ShardResponse, api.ShardChunk:
			c.notifyMessageReceived(data)
			return
		}

		// Unknown structured message — drop silently.
		return
	}
}

// processMessage processes a message from the server
func (c *Client) processMessage(msg *api.Message) {
	switch msg.Type {
	case api.WaitingForPeer:
		c.setState(StateWaiting)

	case api.AssignedAsLeader:
		_, err := msg.GetAssignedAsLeaderData()
		if err != nil {
			c.notifyError(fmt.Errorf("Failed to parse assigned as leader: %w", err))
			return
		}
		c.setState(StateLeader)

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

		// PeerAssignment from STUN always points a member to the leader,
		// or tells the leader about a new member. We can identify the leader
		// peer on the member side: if our state is not leader, the assigned
		// peer IS the leader.
		peerInfo := &PeerInfo{
			Address: peerAddr,
			ID:      data.PeerID,
		}

		c.mutex.Lock()
		state := c.state
		if state != StateLeader {
			peerInfo.IsLeader = true
		}
		c.peers[data.PeerID] = peerInfo
		c.mutex.Unlock()

		if state != StateLeader {
			c.setState(StatePaired)
		}

		c.notifyPeerAssigned(peerInfo)

		if state == StateLeader {
			c.leaderHandleJoiner(c.peers[data.PeerID])
		}

	case api.ServerError:
		data, err := msg.GetServerErrorData()
		if err != nil {
			c.notifyError(fmt.Errorf("failed to parse server error: %w", err))
			return
		}

		serverErr := fmt.Errorf("server error [%s]: %s", data.ErrorCode, data.ErrorMessage)

		c.mutex.Lock()
		ch := c.registrationDone
		c.registrationDone = nil
		c.mutex.Unlock()

		if ch != nil {
			ch <- serverErr // fail the ConnectToStun call
		} else {
			c.notifyError(serverErr)
		}

	case api.RegisterSuccess:
		data, err := msg.GetRegisterSuccessData()
		if err != nil {
			c.notifyError(fmt.Errorf("failed to parse register success data: %w", err))
			return
		}

		c.mutex.Lock()
		c.id = data.ID
		c.queuePosition = data.QueuePosition
		ch := c.registrationDone
		c.registrationDone = nil // signal only once
		c.mutex.Unlock()

		if ch != nil {
			ch <- nil
		}

	default:
		c.notifyError(fmt.Errorf("unknown message type: %s", msg.Type))
	}
}
