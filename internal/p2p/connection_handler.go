package p2p

/*

This file is for code pertaining to ensuring the connection between peers stays strong.
ping pongs go here and managing that goes with that goes here. Any code that is for purely
managing the network connection between peers goes here. NOT CODE THAT USES THAT NETWORK!

*/

import (
	"fmt"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

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

	msg := api.NewPeerPingMessage(api.NewSignature(c.id))
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

	msg := api.NewPeerPongMessage(api.NewSignature(c.id))
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

				msg := api.NewClientPingMessage(api.NewSignature(c.id))
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
