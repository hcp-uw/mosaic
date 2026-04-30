package p2p

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

const (
	sessionEncryptedMagic = 0x02 // first byte of an AES-256-GCM wrapped frame
)

// PeerInfo holds information about the assigned peer
type PeerInfo struct {
	Address      *net.UDPAddr
	Conn         net.PacketConn // direct *net.UDPConn or a TURN relay PacketConn
	ID           string
	IsLeader     bool      // true when this peer is the network leader
	LastPeerPong time.Time
	ViaTURN      bool // true when Conn routes through the TURN relay

	// Session encryption — set after X25519 handshake completes.
	SessionKey      [32]byte
	HandshakeDone   bool
	EphemeralPrivKey []byte // our ephemeral X25519 private key; cleared after handshake
}

// sealForPeer wraps data in AES-256-GCM using the peer's session key.
// Format: [0x02][12-byte nonce][ciphertext+tag]
func (peer *PeerInfo) sealForPeer(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(peer.SessionKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 1+len(nonce)+len(ct))
	out[0] = sessionEncryptedMagic
	copy(out[1:], nonce)
	copy(out[1+len(nonce):], ct)
	return out, nil
}

// openFromPeer decrypts a session-encrypted frame. Returns the inner plaintext.
func (peer *PeerInfo) openFromPeer(frame []byte) ([]byte, error) {
	if len(frame) < 1+12+16 {
		return nil, fmt.Errorf("frame too short")
	}
	block, err := aes.NewCipher(peer.SessionKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := frame[1 : 1+gcm.NonceSize()]
	ct := frame[1+gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// writeToPeer serializes msg, encrypts it if the handshake is done, and sends it.
func (c *Client) writeToPeer(peer *PeerInfo, msg *api.Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return err
	}
	return c.writeRawToPeer(peer, data)
}

// writeRawToPeer encrypts data (if handshake done) and writes it to the peer.
func (c *Client) writeRawToPeer(peer *PeerInfo, data []byte) error {
	if peer.HandshakeDone {
		encrypted, err := peer.sealForPeer(data)
		if err != nil {
			return fmt.Errorf("session encrypt failed: %w", err)
		}
		data = encrypted
	}
	_, err := peer.Conn.WriteTo(data, peer.Address)
	return err
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
	if state == StateDisconnected {
		return fmt.Errorf("client disconnected")
	}
	return c.writeToPeer(peerInfo, message)
}

func (c *Client) SendToAllPeers(message *api.Message) error {
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
		if err := c.writeToPeer(peer, message); err != nil {
			return err
		}
	}
	return nil
}

// SendRawToPeer sends raw bytes to a single peer by ID, encrypting if the
// session handshake is complete.
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
	return c.writeRawToPeer(peer, data)
}

// SendRawToAllPeers sends raw bytes to all connected peers, encrypting per peer
// if their session handshake is complete.
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
		if err := c.writeRawToPeer(peer, data); err != nil {
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

// ConnectToPeer attempts to establish direct connection to assigned peer using UDP hole punching.
// It also generates an ephemeral X25519 keypair and sends HandshakeInit so both sides can
// derive a shared AES-256-GCM session key for all subsequent messages.
func (c *Client) ConnectToPeer(peer *PeerInfo) error {
	c.mutex.Lock()

	if peer == nil {
		c.mutex.Unlock()
		return fmt.Errorf("no peer assigned")
	}
	if c.serverConn == nil {
		c.mutex.Unlock()
		return fmt.Errorf("not connected to server")
	}

	// Generate ephemeral X25519 keypair for the session handshake.
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		c.mutex.Unlock()
		return fmt.Errorf("failed to generate handshake key: %w", err)
	}

	c.peers[peer.ID] = peer
	c.peers[peer.ID].Conn = c.serverConn
	c.peers[peer.ID].LastPeerPong = time.Now()
	c.peers[peer.ID].EphemeralPrivKey = ephPriv.Bytes()
	if c.state != StateLeader {
		c.setState(StateConnectedToPeer)
	}

	peerAddr := c.peers[peer.ID].Address
	peerID := peer.ID
	pubKeyBytes := ephPriv.PublicKey().Bytes()
	myID := c.id
	c.mutex.Unlock()

	go c.establishPeerConnection(peerAddr)

	// Send HandshakeInit after punch packets have had a chance to open the path.
	go func() {
		time.Sleep(300 * time.Millisecond)
		msg := api.NewHandshakeInitMessage(myID, pubKeyBytes)
		// Send directly (plaintext) — session key doesn't exist yet.
		c.mutex.RLock()
		p := c.peers[peerID]
		c.mutex.RUnlock()
		if p != nil && p.Conn != nil {
			data, _ := msg.Serialize()
			p.Conn.WriteTo(data, p.Address) //nolint:errcheck — best-effort
		}
	}()

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

	punchMessage := []byte("STUN_PUNCH")
	for range 3 {
		_, err := peerConn.WriteTo(punchMessage, peerAddr)
		if err != nil {
			c.notifyError(fmt.Errorf("failed to send punch packet: %w", err))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
