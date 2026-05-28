package p2p

/*

TURN relay fallback for peers that cannot establish direct UDP connections.

Flow:
  1. ConnectToPeer sets up direct UDP and starts hole-punching.
  2. If no pong arrives within turnFallbackTimeout, ConnectViaTURN is called.
  3. ConnectViaTURN dials the relay server (internal/turn), wraps the relay
     client in a relayPacketConn, and replaces PeerInfo.Conn with it.
  4. A dedicated receive goroutine reads from the relay and forwards frames to
     processPeerMessage — the rest of the stack sees no difference.
  5. When the peer that receives TURNRelayAddr calls connectAsRelayFollower,
     it dials the same relay server from its own side, establishing the
     bidirectional session without any IP:port permission checks.
  6. Every stunRetryInterval the ping routine calls tryPromoteToDirectUDP for
     each TURN-relayed peer. If a hole-punch succeeds (pong arrives on serverConn)
     the peer is promoted back to direct UDP and the relay is released.

Why this fixes symmetric NAT:
  The relay server identifies peers by peer ID, not by IP:port. Each side
  connects independently using a fresh UDP socket; the server records both
  source addresses and forwards data between them. No permissions to match,
  no port-mismatch from symmetric NAT remapping.

*/

import (
	"context"
	"crypto/ecdh"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/turn"
)

// forceTURN forces all peers onto the TURN relay immediately, skipping
// direct UDP hole-punching. Set to true to simulate symmetric NAT in testing.
const forceTURN = false

// turnFallbackTimeout is how long we wait for a direct pong before falling
// back to the relay. 1s when forceTURN is set (instant fallback for testing);
// 25s in production so peers get at least two full ping cycles before relay.
var turnFallbackTimeout = func() time.Duration {
	if forceTURN {
		return 1 * time.Second
	}
	return 25 * time.Second
}()

// relayDialTimeout is how long ConnectViaTURN / connectAsRelayFollower waits
// for the other side to register with the relay server before giving up.
const relayDialTimeout = 15 * time.Second

// relayState owns the relay client and the PacketConn adapter for one peer.
type relayState struct {
	client    *turn.Client
	relayConn net.PacketConn // relayPacketConn wrapping the client
	peerAddr  *net.UDPAddr   // peer's original STUN-reported address
}

func (rs *relayState) close() {
	if rs.relayConn != nil {
		rs.relayConn.Close()
	}
}

// relayPacketConn adapts a turn.Client to the net.PacketConn interface so the
// rest of the p2p stack can write to a relay peer exactly like a direct peer.
// WriteTo ignores the addr argument — the target is baked into the relay client.
// ReadFrom blocks on the client's RecvChan with an optional read deadline.
type relayPacketConn struct {
	client   *turn.Client
	peerAddr *net.UDPAddr // returned as the "from" address by ReadFrom

	mu           sync.Mutex
	readDeadline time.Time
}

func (r *relayPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	if err := r.client.Send(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (r *relayPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	r.mu.Lock()
	dl := r.readDeadline
	r.mu.Unlock()

	var timer <-chan time.Time
	if !dl.IsZero() {
		d := time.Until(dl)
		if d <= 0 {
			return 0, nil, &relayTimeoutError{}
		}
		timer = time.NewTimer(d).C
	}

	select {
	case data, ok := <-r.client.RecvChan():
		if !ok {
			return 0, nil, io.EOF
		}
		n := copy(b, data)
		return n, r.peerAddr, nil
	case <-timer:
		return 0, nil, &relayTimeoutError{}
	}
}

func (r *relayPacketConn) SetDeadline(t time.Time) error {
	r.mu.Lock()
	r.readDeadline = t
	r.mu.Unlock()
	return nil
}

func (r *relayPacketConn) SetReadDeadline(t time.Time) error  { return r.SetDeadline(t) }
func (r *relayPacketConn) SetWriteDeadline(_ time.Time) error { return nil }
func (r *relayPacketConn) LocalAddr() net.Addr                { return r.client.LocalAddr() }
func (r *relayPacketConn) Close() error                       { r.client.Close(); return nil }

// relayTimeoutError satisfies net.Error so that deadline-based read loops
// distinguish timeout from fatal errors and keep the connection alive.
type relayTimeoutError struct{}

func (e *relayTimeoutError) Error() string   { return "relay: i/o timeout" }
func (e *relayTimeoutError) Timeout() bool   { return true }
func (e *relayTimeoutError) Temporary() bool { return true }

// ConnectViaTURN is called when direct hole-punching has timed out for a peer.
// It dials the relay server, wraps the relay client in a relayPacketConn,
// replaces PeerInfo.Conn, and starts a receive goroutine.
func (c *Client) ConnectViaTURN(peerID string) error {
	if c.turnAddr == "" {
		return fmt.Errorf("TURN: no relay server configured")
	}

	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()

	if peer == nil {
		return fmt.Errorf("TURN: peer %s not found", peerID)
	}

	fmt.Printf("[TURN] hole-punch timed out for %s — dialing relay\n", peerID[:minPeerIDLen(peerID)])

	dialCtx, dialCancel := context.WithTimeout(c.ctx, relayDialTimeout)
	defer dialCancel()

	relayClient, err := turn.Dial(dialCtx, c.turnAddr, c.id, peerID)
	if err != nil {
		return fmt.Errorf("TURN: relay dial failed for %s: %w", peerID, err)
	}

	rpc := &relayPacketConn{client: relayClient, peerAddr: peer.Address}
	rs := &relayState{
		client:    relayClient,
		relayConn: rpc,
		peerAddr:  peer.Address,
	}

	c.mutex.Lock()
	peer = c.peers[peerID] // re-read under write lock
	if peer == nil {
		c.mutex.Unlock()
		rs.close()
		return fmt.Errorf("TURN: peer %s disappeared", peerID)
	}
	if peer.ViaTURN {
		// Duplicate concurrent ConnectViaTURN call — discard this allocation.
		c.mutex.Unlock()
		rs.close()
		return nil
	}
	peer.Conn = rs.relayConn
	peer.ViaTURN = true
	peer.LastPeerPong = time.Now()
	c.mutex.Unlock()

	go c.handleTURNMessages(peerID, rs)

	fmt.Printf("[TURN] relay active for peer %s via %s\n", peerID[:minPeerIDLen(peerID)], c.turnAddr)

	// Notify the peer of the relay server address via the STUN forwarding path.
	// We send c.turnAddr (the relay server address) rather than a per-peer
	// allocated relay port. The receiver's connectAsRelayFollower dials the
	// same server and the server matches both sides by peer ID.
	fwdMsg := api.NewTURNRelayAddrFwdMessage(c.id, api.TURNRelayAddrFwdData{
		TargetPeerID: peerID,
		RelayAddr:    c.turnAddr,
	})
	if err := c.sendToServer(fwdMsg); err != nil {
		fmt.Printf("[TURN] could not forward relay addr for %s via STUN: %v\n", peerID[:minPeerIDLen(peerID)], err)
	}
	return nil
}

// connectAsRelayFollower is called when we receive a TURNRelayAddr message
// telling us the relay server address that the initiator peer connected to.
// We dial the same relay server from our side, completing the bidirectional
// relay session.
func (c *Client) connectAsRelayFollower(initiatorID, relayServerAddr string) {
	c.mutex.RLock()
	peer, ok := c.peers[initiatorID]
	c.mutex.RUnlock()

	if !ok || peer == nil {
		fmt.Printf("[TURN] follower: initiator peer %s not found\n", initiatorID[:minPeerIDLen(initiatorID)])
		return
	}

	// Guard against concurrent or duplicate follower dials.
	c.mutex.Lock()
	if peer.ViaTURN {
		c.mutex.Unlock()
		return
	}
	c.mutex.Unlock()

	fmt.Printf("[TURN] dialing relay as follower for %s via %s\n",
		initiatorID[:minPeerIDLen(initiatorID)], relayServerAddr)

	dialCtx, dialCancel := context.WithTimeout(c.ctx, relayDialTimeout)
	defer dialCancel()

	relayClient, err := turn.Dial(dialCtx, relayServerAddr, c.id, initiatorID)
	if err != nil {
		fmt.Printf("[TURN] follower dial failed for %s: %v\n", initiatorID[:minPeerIDLen(initiatorID)], err)
		return
	}

	rpc := &relayPacketConn{client: relayClient, peerAddr: peer.Address}
	rs := &relayState{
		client:    relayClient,
		relayConn: rpc,
		peerAddr:  peer.Address,
	}

	c.mutex.Lock()
	peer = c.peers[initiatorID]
	if peer == nil {
		c.mutex.Unlock()
		rs.close()
		return
	}
	if peer.ViaTURN {
		c.mutex.Unlock()
		rs.close()
		return
	}
	peer.Conn = rs.relayConn
	peer.ViaTURN = true
	peer.LastPeerPong = time.Now()
	ephPrivBytes := peer.EphemeralPrivKey
	myID := c.id
	quicPort := c.quicPort
	c.mutex.Unlock()

	go c.handleTURNMessages(initiatorID, rs)

	fmt.Printf("[TURN] follower relay active for peer %s\n", initiatorID[:minPeerIDLen(initiatorID)])

	// Re-send HandshakeInit over the relay path so both sides can complete the
	// X25519 key exchange (the original init was dropped by NAT).
	if len(ephPrivBytes) > 0 {
		go func() {
			ephPriv, err := ecdh.X25519().NewPrivateKey(ephPrivBytes)
			if err != nil {
				return
			}
			initMsg := api.NewHandshakeInitMessage(myID, ephPriv.PublicKey().Bytes(), quicPort)
			data, _ := initMsg.Serialize()
			rpc.WriteTo(data, nil) //nolint:errcheck — best-effort
		}()
	}
}

// handleTURNMessages reads frames arriving on the relay and routes them through
// the normal peer message pipeline. Runs until the relay closes or the client
// context is cancelled, then releases the relay connection.
func (c *Client) handleTURNMessages(peerID string, rs *relayState) {
	defer func() {
		rs.close()
		// Only clear Conn if it still points at THIS relay's PacketConn.
		c.mutex.Lock()
		if peer, ok := c.peers[peerID]; ok && peer.Conn == rs.relayConn {
			peer.Conn = nil
			peer.ViaTURN = false
		}
		c.mutex.Unlock()
		fmt.Printf("[TURN] relay closed for peer %s\n", peerID[:minPeerIDLen(peerID)])
	}()

	buf := make([]byte, 65507)
	for {
		if c.ctx.Err() != nil {
			return
		}
		rs.relayConn.SetReadDeadline(time.Now().Add(35 * time.Second))
		n, _, err := rs.relayConn.ReadFrom(buf)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Idle deadline — reset and keep the relay alive.
				continue
			}
			c.notifyError(fmt.Errorf("TURN recv error for peer %s: %w", peerID, err))
			return
		}
		msg := make([]byte, n)
		copy(msg, buf[:n])
		c.processPeerMessage(msg, rs.peerAddr)
	}
}

// tryPromoteToDirectUDP sends hole-punch packets on the real serverConn for a
// relay-relayed peer. If the peer responds directly, the normal ping/pong
// machinery updates LastPeerPong and promotion happens naturally on the next
// ping cycle. Called from the ping routine.
func (c *Client) tryPromoteToDirectUDP(peerID string) {
	c.mutex.RLock()
	peer := c.peers[peerID]
	serverConn := c.serverConn
	c.mutex.RUnlock()

	if peer == nil || !peer.ViaTURN || serverConn == nil {
		return
	}

	punch := []byte("STUN_PUNCH")
	for range 3 {
		serverConn.WriteToUDP(punch, peer.Address) //nolint:errcheck — best-effort
		time.Sleep(100 * time.Millisecond)
	}
}

// minPeerIDLen returns the min of 8 and len(s), used for safe log truncation.
func minPeerIDLen(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}
