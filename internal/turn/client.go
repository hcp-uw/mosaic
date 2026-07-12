package turn

import (
	"context"
	"fmt"
	"net"
	"time"
)

const (
	clientRecvBuf  = 64
	pingInterval   = 20 * time.Second
	readyTimeout   = 15 * time.Second
	clientReadSize = 65507
)

// Client is the peer-side relay client. It dials the relay server, registers
// the caller's peer ID, and requests a relay session to a target peer.
// Once both sides have connected, the server sends RelayReady and bidirectional
// forwarding begins.
type Client struct {
	conn         *net.UDPConn
	serverAddr   *net.UDPAddr
	myPeerID     string
	targetPeerID string
	recvCh       chan []byte
	ctx          context.Context
	cancel       context.CancelFunc
}

// Dial connects to the relay server at serverAddr, registers myPeerID, and
// requests a relay session to targetPeerID. It blocks until both sides have
// registered (RelayReady received) or ctx is cancelled.
//
// serverAddr format: "host:port" e.g. "178.128.151.84:3479"
func Dial(ctx context.Context, serverAddr, myPeerID, targetPeerID string) (*Client, error) {
	srvAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("relay: resolve server %s: %w", serverAddr, err)
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, fmt.Errorf("relay: open socket: %w", err)
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, readyTimeout)
	defer dialCancel()

	// Register and request the session.
	if _, err := conn.WriteToUDP(EncodeRegister(myPeerID), srvAddr); err != nil {
		conn.Close()
		return nil, fmt.Errorf("relay: register send: %w", err)
	}
	if _, err := conn.WriteToUDP(EncodeConnect(myPeerID, targetPeerID), srvAddr); err != nil {
		conn.Close()
		return nil, fmt.Errorf("relay: connect send: %w", err)
	}

	// Wait for RelayReady.
	buf := make([]byte, clientReadSize)
	for {
		if dialCtx.Err() != nil {
			conn.Close()
			return nil, fmt.Errorf("relay: timed out waiting for RelayReady from %s", serverAddr)
		}
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Resend in case the server hasn't seen our connect yet.
				conn.WriteToUDP(EncodeConnect(myPeerID, targetPeerID), srvAddr) //nolint:errcheck
				continue
			}
			conn.Close()
			return nil, fmt.Errorf("relay: read during handshake: %w", err)
		}
		if n == 0 {
			continue
		}
		if buf[0] == OpRelayReady {
			break
		}
		// Ignore any other frames during the handshake phase.
	}

	// The client's lifetime is independent from the dial context (which only
	// governs how long we wait for RelayReady). The client lives until Close()
	// is explicitly called by the owner — typically when the relay is torn down.
	clientCtx, clientCancel := context.WithCancel(context.Background())
	c := &Client{
		conn:         conn,
		serverAddr:   srvAddr,
		myPeerID:     myPeerID,
		targetPeerID: targetPeerID,
		recvCh:       make(chan []byte, clientRecvBuf),
		ctx:          clientCtx,
		cancel:       clientCancel,
	}
	go c.readLoop()
	go c.pingLoop()
	return c, nil
}

// Send delivers data to the target peer via the relay server.
func (c *Client) Send(data []byte) error {
	_, err := c.conn.WriteToUDP(EncodeData(c.targetPeerID, data), c.serverAddr)
	return err
}

// RecvChan returns the channel on which decoded inbound payloads arrive.
// The channel is closed when the client is closed.
func (c *Client) RecvChan() <-chan []byte {
	return c.recvCh
}

// LocalAddr returns the local UDP address of the underlying socket.
func (c *Client) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// Close shuts down the relay client.
func (c *Client) Close() {
	c.cancel()
	c.conn.Close()
}

func (c *Client) readLoop() {
	defer close(c.recvCh)
	buf := make([]byte, clientReadSize)
	for {
		if c.ctx.Err() != nil {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}
		if n == 0 {
			continue
		}
		switch buf[0] {
		case OpRelayData:
			_, data, err := DecodeData(buf[1:n])
			if err != nil {
				continue
			}
			payload := make([]byte, len(data))
			copy(payload, data)
			// Non-blocking send: drop if channel is full rather than stalling the loop.
			select {
			case c.recvCh <- payload:
			default:
			}
		case OpRelayPing:
			c.conn.WriteToUDP(EncodePong(), c.serverAddr) //nolint:errcheck
		case OpRelayReady:
			// Server may resend ready; safe to ignore after initial Dial.
		}
	}
}

func (c *Client) pingLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.conn.WriteToUDP(EncodePing(), c.serverAddr) //nolint:errcheck
		}
	}
}
