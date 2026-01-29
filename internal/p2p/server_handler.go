package p2p

/*

This file is for connection logic that pertains to Client-Server Connection

*/

import (
	"fmt"
	"net"
)

func (c *Client) ConnectToStun() error {
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

	c.serverConn = conn
	c.setState(StateConnecting)

	// Start message handling
	go c.handleMessages()

	// Start ping routine
	go c.pingRoutine()

	// Register with server
	return c.register()
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
	c.peerConn = nil

	c.setState(StateDisconnected)
	c.peerInfo = nil

	return nil
}

