package p2p

/*

This file manages the liveness of the P2P network connections:
  - Leader pings STUN every 10s. Three consecutive failures → background reconnect loop.
  - All nodes (leader and members) ping each peer every 10s.
  - Any peer that hasn't ponged in 30s is evicted.
  - If an evicted peer was the leader, the member re-registers with STUN
    so STUN can elect a new leader from whoever is still active.

Members deliberately do NOT ping STUN after pairing — only the leader does.
This keeps STUN traffic minimal and reflects Mosaic's decentralized design.

*/

import (
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

const (
	peerPingInterval  = 10 * time.Second
	peerPongTimeout   = 30 * time.Second
	maxStunFailures   = 3
	stunRetryInterval = 30 * time.Second
)

// pingRoutine runs for the lifetime of the connection.
// It fires every peerPingInterval and handles both STUN keepalive (leader only)
// and peer-to-peer liveness pings (all nodes).
func (c *Client) pingRoutine() {
	ticker := time.NewTicker(peerPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mutex.RLock()
			state := c.state
			c.mutex.RUnlock()

			if state == StateDisconnected {
				return
			}

			c.tickStunPing(state)
			c.tickPeerPings(state)
		}
	}
}

// tickStunPing sends a STUN keepalive when appropriate:
//   - Always: while still connecting or waiting for a peer
//   - Leader only: after pairing (members stop pinging STUN once they have peers)
func (c *Client) tickStunPing(state ClientState) {
	switch state {
	case StateConnecting, StateWaiting:
		// Not yet paired — ping STUN to stay registered while waiting.
		msg := api.NewClientPingMessage(api.NewSignature(c.id))
		if err := c.sendToServer(msg); err != nil {
			c.notifyError(fmt.Errorf("STUN ping failed while waiting: %w", err))
		}

	case StateLeader:
		msg := api.NewClientPingMessage(api.NewSignature(c.id))
		if err := c.sendToServer(msg); err != nil {
			c.mutex.Lock()
			c.stunFailCount++
			failing := c.stunFailCount >= maxStunFailures
			reconnecting := c.stunReconnecting
			c.mutex.Unlock()

			if failing && !reconnecting {
				c.notifyError(fmt.Errorf("STUN unreachable after %d failures — retrying in background", maxStunFailures))
				go c.reconnectToStun()
			}
		} else {
			c.mutex.Lock()
			c.stunFailCount = 0
			c.mutex.Unlock()
		}

	// StatePaired, StateConnectedToPeer: member does not ping STUN.
	}
}

// tickPeerPings sends a ping to every connected peer and evicts any that have
// timed out. If the evicted peer was the network leader, triggers STUN re-registration.
func (c *Client) tickPeerPings(state ClientState) {
	type pingTarget struct {
		conn *net.UDPConn
		addr *net.UDPAddr
	}

	c.mutex.Lock()
	now := time.Now()
	var toSend []pingTarget
	deadLeaderFound := false

	for id, peer := range c.peers {
		if peer.Conn == nil {
			continue
		}
		// Evict peers that haven't ponged within the timeout window.
		// LastPeerPong is initialised to time.Now() in ConnectToPeer, so the
		// timeout clock starts from the moment the connection is established.
		if now.Sub(peer.LastPeerPong) > peerPongTimeout {
			if peer.IsLeader {
				deadLeaderFound = true
			}
			delete(c.peers, id)
			c.notifyError(fmt.Errorf("peer %s timed out — evicted", id))
			continue
		}
		toSend = append(toSend, pingTarget{conn: peer.Conn, addr: peer.Address})
	}
	c.mutex.Unlock()

	// Send pings outside the lock to avoid holding it during I/O.
	for _, t := range toSend {
		msg := api.NewPeerPingMessage(api.NewSignature(c.id))
		if data, err := msg.Serialize(); err == nil {
			t.conn.WriteToUDP(data, t.addr) //nolint:errcheck — best-effort UDP
		}
	}

	// A member whose leader just died re-registers with STUN.
	// STUN will elect whoever has the lowest queue position among active clients.
	if deadLeaderFound && state != StateLeader {
		go c.reregisterWithStun()
	}
}

// reconnectToStun is started by the leader when STUN becomes unreachable.
// It retries registration every stunRetryInterval until the context is cancelled.
func (c *Client) reconnectToStun() {
	c.mutex.Lock()
	if c.stunReconnecting {
		c.mutex.Unlock()
		return
	}
	c.stunReconnecting = true
	c.mutex.Unlock()

	defer func() {
		c.mutex.Lock()
		c.stunReconnecting = false
		c.stunFailCount = 0
		c.mutex.Unlock()
	}()

	ticker := time.NewTicker(stunRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.register(); err != nil {
				c.notifyError(fmt.Errorf("STUN reconnect attempt failed: %w", err))
				continue
			}
			c.notifyError(fmt.Errorf("STUN reconnected and re-registered"))
			return
		}
	}
}

// reregisterWithStun is called by a member when its leader peer dies.
// It re-registers immediately, then retries every stunRetryInterval on failure.
// STUN will either promote this node as the new leader (if it has the lowest
// queue position among remaining active clients) or pair it with the new leader.
func (c *Client) reregisterWithStun() {
	c.mutex.Lock()
	c.setState(StateWaiting)
	c.mutex.Unlock()

	// Try immediately.
	if err := c.register(); err == nil {
		return
	}

	ticker := time.NewTicker(stunRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.register(); err != nil {
				c.notifyError(fmt.Errorf("STUN re-register attempt failed: %w", err))
				continue
			}
			return
		}
	}
}

// sendPeerPing sends a ping to a peer by ID.
func (c *Client) sendPeerPing(peerID string) error {
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()

	if peer == nil || peer.Conn == nil {
		return fmt.Errorf("peer %s not connected", peerID)
	}

	msg := api.NewPeerPingMessage(api.NewSignature(c.id))
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize peer ping: %w", err)
	}

	_, err = peer.Conn.WriteToUDP(data, peer.Address)
	return err
}

// sendPeerPong sends a pong response to a peer identified by their public key / ID.
func (c *Client) sendPeerPong(peerId string) error {
	c.mutex.RLock()
	peer := c.peers[peerId]
	c.mutex.RUnlock()

	if peer == nil || peer.Conn == nil {
		return fmt.Errorf("peer %s not connected", peerId)
	}

	msg := api.NewPeerPongMessage(api.NewSignature(c.id))
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize peer pong: %w", err)
	}

	_, err = peer.Conn.WriteToUDP(data, peer.Address)
	return err
}
