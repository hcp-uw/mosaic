package p2p

/*

This file is for connection logic that pertains to Client-Server Connection

*/

import (
	"fmt"
	"net"
	"time"
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
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cancel()

	if c.serverConn != nil {
		c.serverConn.Close()
		c.serverConn = nil
	}

	// Note: peerConn is the same as serverConn, so don't close it twice
	c.peers = make(map[string]*PeerInfo)

	c.setState(StateDisconnected)

	return nil
}
