package p2p

/*

This file is for connection logic that pertains to Client-Server Connection

*/

import (
	"fmt"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	quic "github.com/quic-go/quic-go"
)

func (c *Client) ConnectToStun() error {
	c.mutex.Lock()

	if c.state != StateDisconnected {
		c.mutex.Unlock()
		return fmt.Errorf("client already connected or connecting")
	}

	localAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		c.mutex.Unlock()
		return fmt.Errorf("failed to resolve local address: %w", err)
	}

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		c.mutex.Unlock()
		return fmt.Errorf("failed to create UDP socket: %w", err)
	}

	// 32MB receive buffer — handles bursts from 14 parallel shards without drops.
	conn.SetReadBuffer(32 * 1024 * 1024)

	c.serverConn = conn
	c.registrationDone = make(chan error, 1)
	c.setState(StateConnecting)

	// Start QUIC transport on a separate UDP socket so STUN control messages
	// and QUIC data frames never share a port (prevents packet misclassification).
	// Skipped when force-UDP mode is active (dev/testing only).
	if c.forceTransport == "udp" {
		fmt.Println("[QUIC] Disabled (force-UDP mode)")
	} else {
		quicUDP, quicUDPErr := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if quicUDPErr != nil {
			fmt.Printf("[QUIC] Failed to open UDP socket: %v — QUIC disabled, using UDP fallback\n", quicUDPErr)
		} else {
			tlsConf, tlsErr := serverTLSConfig()
			if tlsErr != nil {
				fmt.Printf("[QUIC] TLS setup failed: %v — QUIC disabled, using UDP fallback\n", tlsErr)
				quicUDP.Close()
			} else {
				qt := &quic.Transport{Conn: quicUDP}
				ln, lnErr := qt.Listen(tlsConf, defaultQUICConfig)
				if lnErr != nil {
					fmt.Printf("[QUIC] Listener failed: %v — QUIC disabled, using UDP fallback\n", lnErr)
					qt.Close()
				} else {
					c.quicTr = qt
					c.quicListener = ln
					c.quicPort = quicUDP.LocalAddr().(*net.UDPAddr).Port
					fmt.Printf("[QUIC] Listening on port %d\n", c.quicPort)
					go c.quicAcceptLoop()
				}
			}
		}
	}

	go c.handleMessages()
	go c.pingRoutine()

	if err := c.register(); err != nil {
		c.mutex.Unlock()
		return err
	}

	// Capture the channel before releasing the lock so we can wait outside it.
	regDone := c.registrationDone
	c.mutex.Unlock()

	// Block until the STUN server acknowledges the registration or we time out.
	// This is the real connection confirmation — fire-and-forget UDP is not enough.
	select {
	case err := <-regDone:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timed out waiting for STUN server acknowledgment — server may be unreachable")
	case <-c.ctx.Done():
		return fmt.Errorf("connection cancelled")
	}
}

// Disconnect closes the connection to the server
func (c *Client) DisconnectFromStun() error {
	// Broadcast NodeLeave to all peers before tearing down the socket so they
	// can evict us immediately instead of waiting for the 30-second pong timeout.
	// Use writeToPeer so the message is session-encrypted when a handshake is done.
	c.mutex.RLock()
	leaveMsg := api.NewNodeLeaveMessage(c.id)
	for _, peer := range c.peers {
		c.writeToPeer(peer, leaveMsg) //nolint:errcheck — best-effort
	}
	c.mutex.RUnlock()

	// Tell the STUN server to remove us immediately so that a rapid rejoin does
	// not get paired with our own stale session (self-connection bug).
	deregMsg := api.NewClientDeregisterMessage()
	c.sendToServer(deregMsg) //nolint:errcheck — best-effort; socket still open here

	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cancel()

	if c.quicListener != nil {
		c.quicListener.Close()
		c.quicListener = nil
	}
	if c.quicTr != nil {
		c.quicTr.Close()
		c.quicTr = nil
	}
	c.quicPort = 0

	if c.serverConn != nil {
		c.serverConn.Close()
		c.serverConn = nil
	}

	// Note: peerConn is the same as serverConn, so don't close it twice
	c.peers = make(map[string]*PeerInfo)

	c.setState(StateDisconnected)

	return nil
}
