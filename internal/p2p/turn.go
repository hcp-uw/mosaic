package p2p

/*

TURN relay fallback for peers that cannot establish direct UDP connections.

Flow:
  1. ConnectToPeer sets up direct UDP and starts hole-punching.
  2. If no pong arrives within turnFallbackTimeout, ConnectViaTURN is called.
  3. ConnectViaTURN allocates a relay on the TURN server, creates a permission
     for the peer's address, and replaces PeerInfo.Conn with the relay PacketConn.
  4. A dedicated receive goroutine reads from the relay and forwards frames to
     processPeerMessage — the rest of the stack sees no difference.
  5. Every stunRetryInterval the ping routine calls tryPromoteToDirectUDP for
     each TURN-relayed peer. If a hole-punch succeeds (pong arrives on serverConn)
     the peer is promoted back to direct UDP and the TURN allocation is released.

*/

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/turn/v4"
)

// turnFallbackTimeout is how long we wait for a direct pong before falling
// back to TURN. Must be longer than the hole-punch window (3 × 100ms) but
// short enough that the user isn't left waiting.
const turnFallbackTimeout = 5 * time.Second

// turnState owns a single TURN allocation for one peer.
type turnState struct {
	client    *turn.Client
	relayConn net.PacketConn
	peerAddr  *net.UDPAddr
}

func (ts *turnState) close() {
	if ts.relayConn != nil {
		ts.relayConn.Close()
	}
	if ts.client != nil {
		ts.client.Close()
	}
}

// dialTURN opens a TURN allocation to relay traffic to peerAddr.
// turnServerAddr is "host:port", username/password are the shared credentials.
func dialTURN(turnServerAddr, username, password string, peerAddr *net.UDPAddr) (*turnState, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("TURN: could not open UDP socket: %w", err)
	}

	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: turnServerAddr,
		TURNServerAddr: turnServerAddr,
		Conn:           conn,
		Username:       username,
		Password:       password,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("TURN: client creation failed: %w", err)
	}

	if err := client.Listen(); err != nil {
		client.Close()
		return nil, fmt.Errorf("TURN: listen failed: %w", err)
	}

	relayConn, err := client.Allocate()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("TURN: allocation failed: %w", err)
	}

	if err := client.CreatePermission(peerAddr); err != nil {
		relayConn.Close()
		client.Close()
		return nil, fmt.Errorf("TURN: permission failed for %s: %w", peerAddr, err)
	}

	return &turnState{
		client:    client,
		relayConn: relayConn,
		peerAddr:  peerAddr,
	}, nil
}

// ConnectViaTURN is called when direct hole-punching has timed out for a peer.
// It allocates a TURN relay, swaps PeerInfo.Conn to the relay PacketConn, and
// starts a receive goroutine that feeds incoming frames into processPeerMessage.
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

	fmt.Printf("[TURN] hole-punch timed out for %s — allocating relay\n", peerID)

	ts, err := dialTURN(c.turnAddr, c.turnUsername, c.turnPassword, peer.Address)
	if err != nil {
		return fmt.Errorf("TURN: dial failed: %w", err)
	}

	c.mutex.Lock()
	peer = c.peers[peerID] // re-read under write lock
	if peer == nil {
		c.mutex.Unlock()
		ts.close()
		return fmt.Errorf("TURN: peer %s disappeared", peerID)
	}
	peer.Conn = ts.relayConn
	peer.ViaTURN = true
	peer.LastPeerPong = time.Now()
	c.mutex.Unlock()

	// Receive loop: read from relay, forward to peer message handler.
	go c.handleTURNMessages(peerID, ts)

	fmt.Printf("[TURN] relay active for peer %s via %s\n", peerID, c.turnAddr)
	return nil
}

// handleTURNMessages reads frames arriving on the TURN relay and routes them
// through the normal peer message pipeline. Runs until the relay closes or the
// client context is cancelled, then releases the TURN allocation.
func (c *Client) handleTURNMessages(peerID string, ts *turnState) {
	defer func() {
		ts.close()
		// If the peer is still marked ViaTURN, clear the conn so it looks
		// disconnected — the ping routine will evict it and trigger a retry.
		c.mutex.Lock()
		if peer, ok := c.peers[peerID]; ok && peer.ViaTURN {
			peer.Conn = nil
			peer.ViaTURN = false
		}
		c.mutex.Unlock()
		fmt.Printf("[TURN] relay closed for peer %s\n", peerID)
	}()

	buf := make([]byte, 65507)
	for {
		if c.ctx.Err() != nil {
			return
		}
		ts.relayConn.SetReadDeadline(time.Now().Add(35 * time.Second))
		n, _, err := ts.relayConn.ReadFrom(buf)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.notifyError(fmt.Errorf("TURN recv error for peer %s: %w", peerID, err))
			return
		}
		msg := make([]byte, n)
		copy(msg, buf[:n])
		c.processPeerMessage(msg, ts.peerAddr)
	}
}

// tryPromoteToDirectUDP sends hole-punch packets on the real serverConn for a
// TURN-relayed peer. If the peer responds directly (pong arrives on serverConn)
// the normal ping/pong machinery updates LastPeerPong and the promotion happens
// naturally: ConnectViaTURN is not re-invoked because ViaTURN will be cleared
// when we swap Conn back to serverConn. Called from the ping routine.
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
