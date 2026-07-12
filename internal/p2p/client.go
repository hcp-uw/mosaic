package p2p

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	quic "github.com/quic-go/quic-go"
)

// Client represents a STUN client
type Client struct {
	id            string
	nodeID        string // stable per-machine ID sent to STUN for queue-position persistence
	queuePosition int    // server-assigned position; 1 = leader, 2 = next, etc.

	// Account identity — used to sign the handshake so peers can verify which
	// account a session belongs to (MITM/impersonation protection).
	accountPriv   *ecdsa.PrivateKey
	accountPubDER []byte // PKIX DER of accountPriv's public key
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
	stateCallbacks       []func(ClientState)
	peerCallbacks        []func(*PeerInfo)
	peerLeftCallbacks    []func(string) // called with peer ID when a peer is evicted
	handshakeCallbacks   []func(string) // called with peer ID when handshake completes
	errorCallbacks       []func(error)
	messageCallbacks     []func([]byte)

	// QUIC data transport — separate UDP socket from the STUN control channel.
	quicTr       *quic.Transport
	quicListener *quic.Listener
	quicPort     int // our QUIC listening port; 0 if QUIC not started

	// Shard-probe callbacks (registered by the transfer package to avoid import cycle).
	onShardProbe    func(msg *api.Message)
	onShardProbeAck func(msg *api.Message)
	probeMu         sync.Mutex

	// QUIC shard-stream callbacks (registered by the transfer package).
	// quicBinaryFrameHandler is called SYNCHRONOUSLY from the stream-reader goroutine
	// for every 0x01 frame so the assembly map is fully updated before the
	// stream-done notification fires. quicStreamDoneFn is called in a goroutine.
	quicBinaryFrameHandler func(peerID string, data []byte)
	quicStreamDoneFn       func(peerID string, lastFrame []byte)
	quicCallbackMu         sync.Mutex

	// STUN reconnect state (leader only)
	stunFailCount    int
	stunReconnecting bool

	// TCP relay fallback — used when both direct UDP and TURN are blocked (e.g. university WiFi).
	tcpRelayAddr string
	tcpRelay     *sharedTCPRelay // non-nil once connectTCPRelay succeeds

	// registrationDone is written once by processMessage when RegisterSuccess
	// arrives. ConnectToStun blocks on it so the CLI gets a real confirmation
	// that the STUN server received and acknowledged the registration.
	// Nil after the first registration completes.
	registrationDone chan error
}

// ClientConfig holds client configuration
type ClientConfig struct {
	ServerAddress    string
	TURNAddress      string // optional — empty disables TURN fallback
	TURNUsername     string
	TURNPassword     string
	TCPRelayAddress  string // optional — TCP relay fallback for UDP-blocked networks
	NodeID           string // optional — stable per-machine ID for STUN queue-position persistence
	AccountPrivKey   *ecdsa.PrivateKey // account signing key (required to join — signs the handshake)
	AccountPubKeyDER []byte            // PKIX DER of the account public key
	PingInterval     time.Duration
	ConnectTimeout   time.Duration
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
		tcpRelayAddr:     config.TCPRelayAddress,
		nodeID:           config.NodeID,
		accountPriv:      config.AccountPrivKey,
		accountPubDER:    config.AccountPubKeyDER,
		state:            StateDisconnected,
		peers:            make(map[string]*PeerInfo),
		ctx:              ctx,
		cancel:           cancel,
		stateCallbacks:     make([]func(ClientState), 0),
		peerCallbacks:      make([]func(*PeerInfo), 0),
		peerLeftCallbacks:  make([]func(string), 0),
		handshakeCallbacks: make([]func(string), 0),
		errorCallbacks:     make([]func(error), 0),
		messageCallbacks:   make([]func([]byte), 0),
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

// GetNodeID returns our stable per-machine STUN node ID (empty if not set). This
// is the canonical shard-holder identity recorded in the network manifest.
func (c *Client) GetNodeID() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.nodeID
}

// buildHandshakeInit constructs a HandshakeInit for the given ephemeral public
// key, signed with our account key so the peer can verify which account this
// session belongs to. All three handshake-send sites go through here so every
// HandshakeInit (initial send and NAT/relay resends) carries the signature.
func (c *Client) buildHandshakeInit(ephPub []byte) *api.Message {
	c.mutex.RLock()
	myID, quicPort, nodeID := c.id, c.quicPort, c.nodeID
	accountPub, accountPriv := c.accountPubDER, c.accountPriv
	c.mutex.RUnlock()

	sig, _ := signHandshake(accountPriv, accountPub, ephPub, nodeID)
	return api.NewHandshakeInitMessage(myID, ephPub, quicPort, nodeID, accountPub, sig)
}

// StunNodeIDForPeer returns the stable STUN node ID a connected peer advertised
// during its handshake, or "" if the peer is unknown or hasn't handshaked yet.
func (c *Client) StunNodeIDForPeer(p2pID string) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if peer, ok := c.peers[p2pID]; ok && peer != nil {
		return peer.StunNodeID
	}
	return ""
}

// PeerIDForStunNodeID returns the P2P id of a connected peer whose stable STUN
// node ID matches stunNodeID, or "" if no connected peer matches. Used to route
// a request to a specific holder recorded in the manifest by its stable id.
func (c *Client) PeerIDForStunNodeID(stunNodeID string) string {
	if stunNodeID == "" {
		return ""
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	for id, peer := range c.peers {
		if peer != nil && peer.Conn != nil && peer.StunNodeID == stunNodeID {
			return id
		}
	}
	return ""
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
	msg := api.NewClientRegisterMessage(c.nodeID)
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
			c.processPeerMessage(msg, fromAddr, true)
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

// getPeerByAddr finds a peer by their UDP address (O(n), but peer count is tiny).
func (c *Client) getPeerByAddr(addr *net.UDPAddr) *PeerInfo {
	if addr == nil {
		return nil
	}
	addrStr := addr.String()
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	for _, p := range c.peers {
		if p.Address != nil && p.Address.String() == addrStr {
			return p
		}
	}
	return nil
}

// completeHandshake derives the shared AES-256-GCM session key from the
// received HandshakeInit message and marks the peer's session as ready.
func (c *Client) completeHandshake(msg *api.Message, peer *PeerInfo) {
	if peer == nil {
		return
	}
	d, err := msg.GetHandshakeInitData()
	if err != nil {
		return
	}

	c.mutex.RLock()
	alreadyDone := peer.HandshakeDone
	ephPrivBytes := peer.EphemeralPrivKey
	c.mutex.RUnlock()
	if alreadyDone {
		return // session already established — ignore repeat/spoofed HandshakeInit (no reset)
	}
	if len(ephPrivBytes) == 0 {
		return // we haven't sent our own HandshakeInit yet — drop
	}

	// Verify the handshake is signed by the account it claims. This binds the
	// ephemeral DH key to a real account, so a man-in-the-middle can't substitute
	// its own key under a victim's identity. An unsigned or forged handshake is
	// rejected — no session is established.
	if !verifyHandshake(d.AccountPubKey, d.EphemeralPubKey, d.NodeID, d.Signature) {
		fmt.Printf("[P2P] Rejecting handshake from %s: missing or invalid account signature\n", peer.ID)
		return
	}

	ephPriv, err := ecdh.X25519().NewPrivateKey(ephPrivBytes)
	if err != nil {
		return
	}
	theirPub, err := ecdh.X25519().NewPublicKey(d.EphemeralPubKey)
	if err != nil {
		return
	}
	sharedSecret, err := ephPriv.ECDH(theirPub)
	if err != nil {
		return
	}
	sessionKey, err := hkdf.Key(sha256.New, sharedSecret, nil, "mosaic-session", 32)
	if err != nil {
		return
	}

	c.mutex.Lock()
	copy(peer.SessionKey[:], sessionKey)
	peer.HandshakeDone = true
	peer.EphemeralPrivKey = nil
	peer.QUICPort = d.QUICPort
	peer.StunNodeID = d.NodeID
	peer.AccountPubKey = d.AccountPubKey // verified above — this session is bound to this account
	peerID := peer.ID
	c.mutex.Unlock()

	fmt.Printf("[P2P] Session established with peer %s\n", peerID)
	c.notifyHandshakeDone(peerID)

	// Both sides attempt to dial QUIC. When one peer is behind NAT, only the
	// NAT-side's outgoing dial (to the peer's public IP) succeeds; the public-IP
	// peer's incoming dial hits the NAT-side's LOCAL port and fails silently.
	// When both peers have public IPs both dials succeed, but the first-wins
	// guard in dialQUICToPeer and quicConnAccept prevents duplicate connections.
	if d.QUICPort > 0 {
		go c.dialQUICToPeer(peerID)
	}
}

// plaintextAllowed reports whether a control message is legitimately allowed to
// arrive unencrypted. Only two cases qualify:
//
//   - HandshakeInit: the session key cannot exist yet, so the first exchange is
//     necessarily plaintext. (completeHandshake ignores a repeat once the session
//     is established, so a plaintext HandshakeInit can't reset a live session.)
//   - PeerPing / PeerPong before the peer's session is established: liveness pings
//     flow during connection setup. Once the peer's handshake is done, its pings
//     are sent encrypted and a plaintext one is rejected — closing the spoofed-ping
//     address-rewrite attack against established peers.
//
// Everything else must be authenticated.
func (c *Client) plaintextAllowed(msg *api.Message) bool {
	switch msg.Type {
	case api.HandshakeInit:
		return true
	case api.PeerPing, api.PeerPong:
		c.mutex.RLock()
		peer := c.peers[msg.Sign.PubKey]
		established := peer != nil && peer.HandshakeDone
		c.mutex.RUnlock()
		return !established
	default:
		return false
	}
}

// processPeerMessage processes a message from a peer
func (c *Client) processPeerMessage(data []byte, fromAddr *net.UDPAddr, direct bool) {
	// Filter out STUN punch packets — just discard silently.
	if string(data) == "STUN_PUNCH" {
		return
	}

	// authenticated is true only when the message arrived inside the AES-256-GCM
	// session envelope (0x02) — proof it came from a peer holding the session key.
	// Plaintext (authenticated=false) is trusted only for the handshake bootstrap
	// and pre-session liveness pings; every other control message and all shard
	// frames must be authenticated, otherwise anyone able to send UDP to this port
	// could inject them (spoofed NodeLeave, ShardRequest, ShardDelete, etc.).
	authenticated := false

	// Decrypt session-encrypted frame (magic byte 0x02).
	// Try the peer by source address first; if that fails (e.g. message arrived
	// via a TURN relay whose source port differs from the peer's registered address),
	// fall back to trying every peer with a completed handshake. AES-256-GCM
	// authentication makes false positives cryptographically impossible.
	if len(data) > 0 && data[0] == sessionEncryptedMagic {
		inner, ok := c.decryptFromAnyPeer(data, fromAddr)
		if !ok {
			return
		}
		data = inner
		authenticated = true
	}

	// Binary shard frames start with 0x01 (checked after potential decryption).
	// A raw 0x01 that did not come out of the session envelope is an injection
	// attempt — legitimate shard frames are always sent through the encrypted path.
	if len(data) > 0 && data[0] == 0x01 {
		if !authenticated {
			return
		}
		c.notifyMessageReceived(data)
		return
	}

	// Try to parse as a structured message first.
	if msg, err := api.DeserializeMessage(data); err == nil {
		if !authenticated && !c.plaintextAllowed(msg) {
			return // must-be-authenticated control message arrived in the clear — drop
		}
		switch msg.Type {
		case api.PeerPing:
			// Update peer address to actual source so subsequent sends (pong,
			// shard chunks) reach the peer even under symmetric NAT, where the
			// external port differs from the STUN-reported address.
			if fromAddr != nil {
				c.mutex.Lock()
				if peer, ok := c.peers[msg.Sign.PubKey]; ok && !peer.ViaTURN {
					peer.Address = fromAddr
				}
				c.mutex.Unlock()
			}
			c.sendPeerPong(msg.Sign.PubKey)
			return
		case api.PeerPong:
			var oldConn net.PacketConn
			c.mutex.Lock()
			if peer, ok := c.peers[msg.Sign.PubKey]; ok {
				peer.LastPeerPong = time.Now()
				if direct && peer.ViaTURN {
					// Pong arrived on the direct UDP socket while the peer was
					// relay-relayed — hole-punch succeeded, promote back.
					oldConn = peer.Conn
					peer.Conn = c.serverConn
					peer.ViaTURN = false
					if fromAddr != nil {
						peer.Address = fromAddr
					}
					fmt.Printf("[TURN] promoted peer %s back to direct UDP\n", peer.ID[:minPeerIDLen(peer.ID)])
				} else if fromAddr != nil && !peer.ViaTURN {
					peer.Address = fromAddr
				}
			}
			c.mutex.Unlock()
			if oldConn != nil {
				oldConn.Close()
			}
			return
		case api.PeerTextMessage:
			// Forward the full serialized message so the daemon layer can
			// identify the type and log it properly.
			c.notifyMessageReceived(data)

		case api.NewPeerJoiner:
			c.handleNewPeerJoiner(msg)
		case api.CurrentMembers:
			c.handleCurrentMembers(msg)

		case api.NodeLeave:
			// Peer is disconnecting gracefully — evict immediately.
			// Use the sender's P2P ID from the message (keyed in the map) rather
			// than a source-address lookup, which breaks when NAT remaps the port.
			senderID := msg.Sign.PubKey
			if senderID != "" {
				c.mutex.Lock()
				delete(c.peers, senderID)
				c.mutex.Unlock()
				c.notifyPeerLeft(senderID)
			}
			return

		case api.HandshakeInit:
			// Find peer by their P2P ID (carried in Sign.PubKey) and complete
			// the X25519 key exchange to establish a session key.
			c.mutex.RLock()
			peer := c.peers[msg.Sign.PubKey]
			c.mutex.RUnlock()

			// Symmetric NAT fix: if the packet arrived from an address different
			// from what the STUN server reported, update peer.Address to the
			// actual reachable source. Then resend our own HandshakeInit to that
			// address so the other side can complete its half of the key exchange
			// (our original init went to the STUN-reported address and was dropped).
			if peer != nil && fromAddr != nil {
				var resendPrivBytes []byte
				var resendAddr *net.UDPAddr

				c.mutex.Lock()
				if !peer.ViaTURN && (peer.Address == nil || peer.Address.String() != fromAddr.String()) {
					peer.Address = fromAddr
					if len(peer.EphemeralPrivKey) > 0 {
						resendPrivBytes = make([]byte, len(peer.EphemeralPrivKey))
						copy(resendPrivBytes, peer.EphemeralPrivKey)
						resendAddr = fromAddr
					}
				}
				c.mutex.Unlock()

				if len(resendPrivBytes) > 0 {
					go func() {
						ephPriv, err := ecdh.X25519().NewPrivateKey(resendPrivBytes)
						if err != nil {
							return
						}
						initMsg := c.buildHandshakeInit(ephPriv.PublicKey().Bytes())
						initData, err := initMsg.Serialize()
						if err != nil {
							return
						}
						c.mutex.RLock()
						conn := c.serverConn
						c.mutex.RUnlock()
						if conn != nil {
							conn.WriteToUDP(initData, resendAddr) //nolint:errcheck — best-effort
						}
					}()
				}
			}

			go c.completeHandshake(msg, peer)
			return

		case api.ManifestSync:
			c.notifyMessageReceived(data)
			return
		case api.ShardPush, api.ShardRequest, api.ShardResponse, api.ShardChunk,
			api.ShardStreamDone, api.ShardStreamAck:
			c.notifyMessageReceived(data)
			return
		case api.IdentityAnnounce, api.IdentityChallenge, api.IdentityResponse:
			c.notifyMessageReceived(data)
			return
		case api.ShardDelete:
			c.notifyMessageReceived(data)
			return
		case api.ShardProbe:
			c.probeMu.Lock()
			fn := c.onShardProbe
			c.probeMu.Unlock()
			if fn != nil {
				go fn(msg)
			}
			return
		case api.ShardProbeAck:
			c.probeMu.Lock()
			fn := c.onShardProbeAck
			c.probeMu.Unlock()
			if fn != nil {
				fn(msg) // synchronous: delivers to waiting ProbeShardAtPeer channel
			}
			return
		}

		// Unknown structured message — drop silently.
		return
	}
}

// addRosterPeer adds a peer learned from a membership roster (CurrentMembers or
// NewPeerJoiner) and kicks off a connection to it. It is idempotent: if the peer
// is already known (connecting or connected) it does nothing, so duplicate roster
// information — which now arrives from BOTH the STUN server and the leader — never
// resets an in-progress or established session. First assignment wins.
func (c *Client) addRosterPeer(id string, addr *net.UDPAddr) {
	c.mutex.Lock()
	if id == "" || id == c.id {
		c.mutex.Unlock()
		return
	}
	if _, exists := c.peers[id]; exists {
		c.mutex.Unlock()
		return // already known — don't overwrite / re-handshake
	}
	peerInfo := &PeerInfo{Address: addr, ID: id}
	c.peers[id] = peerInfo
	c.mutex.Unlock()

	c.notifyPeerAssigned(peerInfo)
}

// handleNewPeerJoiner processes a NewPeerJoiner roster message (from the STUN
// server or the leader) announcing a single new member.
func (c *Client) handleNewPeerJoiner(msg *api.Message) {
	data, err := msg.GetNewPeerJoinerData()
	if err != nil {
		c.notifyError(fmt.Errorf("failed to parse new joiner data: %w", err))
		return
	}
	peerAddr, err := net.ResolveUDPAddr("udp", data.JoinerAddress)
	if err != nil {
		c.notifyError(fmt.Errorf("failed to resolve joiner address: %w", err))
		return
	}
	c.addRosterPeer(data.JoinerID, peerAddr)
}

// handleCurrentMembers processes a CurrentMembers roster message listing all
// current members, connecting to any we don't already know.
func (c *Client) handleCurrentMembers(msg *api.Message) {
	data, err := msg.GetCurrentMembersData()
	if err != nil {
		c.notifyError(fmt.Errorf("failed to parse current members: %w", err))
		return
	}
	for id, addr := range data.Members {
		peerAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			c.notifyError(fmt.Errorf("failed to resolve member address: %w", err))
			continue
		}
		c.addRosterPeer(id, peerAddr)
	}
}

// processMessage processes a message from the server
func (c *Client) processMessage(msg *api.Message) {
	switch msg.Type {
	case api.WaitingForPeer:
		c.setState(StateWaiting)

	// Roster messages sent directly by the STUN server (in addition to the
	// leader's gossip) so mesh formation doesn't depend solely on the leader.
	case api.CurrentMembers:
		c.handleCurrentMembers(msg)
	case api.NewPeerJoiner:
		c.handleNewPeerJoiner(msg)

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
		hasRelayAddr := c.tcpRelayAddr != ""
		c.mutex.Unlock()

		// Connect to TCP relay eagerly so both peers are always registered.
		// This ensures the relay can route to us even before direct UDP fails.
		if hasRelayAddr {
			go c.connectTCPRelay()
		}

		if ch != nil {
			ch <- nil
		}

	case api.TURNRelayAddr:
		// The STUN server forwarded the initiator peer's relay server address.
		// Dial the relay server from our side so the server can match both ends
		// by peer ID and begin bidirectional forwarding.
		d, err := msg.GetTURNRelayAddrData()
		if err != nil {
			return
		}
		peerID := msg.Sign.PubKey
		fmt.Printf("[TURN] peer %s relay server → %s; connecting as follower\n",
			peerID[:minPeerIDLen(peerID)], d.RelayAddr)
		go c.connectAsRelayFollower(peerID, d.RelayAddr)

	default:
		c.notifyError(fmt.Errorf("unknown message type: %s", msg.Type))
	}
}
