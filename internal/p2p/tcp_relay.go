package p2p

/*
TCP relay fallback for networks that block UDP entirely (e.g. university WiFi).

Flow:
 1. On ConnectToStun success, the client also dials the TCP relay server and
    registers its peer ID over a persistent TCP connection.
 2. tickPeerPings detects a peer that has never ponged (or whose TURN setup
    failed) and calls ConnectViaTCPRelay.
 3. ConnectViaTCPRelay sets peer.Conn to a tcpRelayPacketConn and peer.ViaTURN
    to true (reuses the flag so ping/pong don't overwrite peer.Address).
 4. A shared read goroutine (handleTCPRelayMessages) reads all incoming frames
    from the TCP relay and routes each to processPeerMessage — the rest of the
    stack sees no difference from the TURN path.
*/

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	tcpRelayTagRegister byte = 0x00
	tcpRelayTagDataSend byte = 0x01
	tcpRelayTagDataRecv byte = 0x02
	tcpRelayTagAck      byte = 0x03

	tcpRelayFallbackTimeout = 30 * time.Second // try TCP relay after this long with no pong
)

// sharedTCPRelay is one TCP connection to the relay server shared by all peers.
type sharedTCPRelay struct {
	mu   sync.Mutex
	conn net.Conn
}

// sendTo writes a DATA frame routing payload to targetID.
func (r *sharedTCPRelay) sendTo(targetID string, payload []byte) error {
	targetBytes := []byte(targetID)
	hdr := make([]byte, 1+2+len(targetBytes)+4)
	hdr[0] = tcpRelayTagDataSend
	binary.BigEndian.PutUint16(hdr[1:], uint16(len(targetBytes)))
	copy(hdr[3:], targetBytes)
	binary.BigEndian.PutUint32(hdr[3+len(targetBytes):], uint32(len(payload)))

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.conn.Write(append(hdr, payload...)); err != nil {
		return fmt.Errorf("tcp relay write: %w", err)
	}
	return nil
}

func (r *sharedTCPRelay) close() {
	r.mu.Lock()
	if r.conn != nil {
		r.conn.Close()
	}
	r.mu.Unlock()
}

// tcpRelayPacketConn implements net.PacketConn for a single peer over the
// shared TCP relay. WriteTo ignores addr and always routes to peerID.
type tcpRelayPacketConn struct {
	relay  *sharedTCPRelay
	peerID string
}

func (c *tcpRelayPacketConn) WriteTo(data []byte, _ net.Addr) (int, error) {
	if err := c.relay.sendTo(c.peerID, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// ReadFrom is not used — incoming frames are handled by handleTCPRelayMessages.
func (c *tcpRelayPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, fmt.Errorf("tcpRelayPacketConn: ReadFrom not supported (use handleTCPRelayMessages)")
}

func (c *tcpRelayPacketConn) Close() error                       { return nil }
func (c *tcpRelayPacketConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *tcpRelayPacketConn) SetDeadline(_ time.Time) error      { return nil }
func (c *tcpRelayPacketConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *tcpRelayPacketConn) SetWriteDeadline(_ time.Time) error { return nil }

// connectTCPRelay dials the relay server, sends a REGISTER frame, and waits
// for ACK. Stores the result in c.tcpRelay and starts the read goroutine.
// Safe to call multiple times — subsequent calls are no-ops if already connected.
func (c *Client) connectTCPRelay() error {
	c.mutex.Lock()
	if c.tcpRelay != nil {
		c.mutex.Unlock()
		return nil
	}
	addr := c.tcpRelayAddr
	myID := c.id
	c.mutex.Unlock()

	if addr == "" {
		return fmt.Errorf("tcp relay: no relay address configured")
	}

	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp",
		addr,
		&tls.Config{InsecureSkipVerify: true}, //nolint:gosec — self-signed cert; payload is AES-256-GCM encrypted end-to-end
	)
	if err != nil {
		return fmt.Errorf("tcp relay: dial %s: %w", addr, err)
	}

	// Send REGISTER frame.
	idBytes := []byte(myID)
	reg := make([]byte, 1+2+len(idBytes))
	reg[0] = tcpRelayTagRegister
	binary.BigEndian.PutUint16(reg[1:], uint16(len(idBytes)))
	copy(reg[3:], idBytes)
	if _, err := conn.Write(reg); err != nil {
		conn.Close()
		return fmt.Errorf("tcp relay: register write: %w", err)
	}

	// Wait for ACK.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil || ack[0] != tcpRelayTagAck {
		conn.Close()
		return fmt.Errorf("tcp relay: no ACK from relay server")
	}
	conn.SetReadDeadline(time.Time{})

	relay := &sharedTCPRelay{conn: conn}

	c.mutex.Lock()
	if c.tcpRelay != nil {
		// Lost a race — another goroutine connected first.
		c.mutex.Unlock()
		conn.Close()
		return nil
	}
	c.tcpRelay = relay
	c.mutex.Unlock()

	fmt.Printf("[TCPRelay] Connected to relay at %s\n", addr)
	go c.handleTCPRelayMessages(relay)
	return nil
}

// handleTCPRelayMessages reads DATA frames from the shared TCP relay connection
// and routes each to processPeerMessage. Runs until the connection closes or the
// client context is cancelled.
func (c *Client) handleTCPRelayMessages(relay *sharedTCPRelay) {
	defer func() {
		relay.close()
		c.mutex.Lock()
		if c.tcpRelay == relay {
			c.tcpRelay = nil
		}
		c.mutex.Unlock()
		fmt.Println("[TCPRelay] Connection closed")
	}()

	hdrBuf := make([]byte, 1+2+512+4) // tag + 2-byte len + max 512-byte ID + 4-byte data len
	dataBuf := make([]byte, 1<<20)

	for {
		if c.ctx.Err() != nil {
			return
		}

		// Read tag.
		var tag [1]byte
		if _, err := io.ReadFull(relay.conn, tag[:]); err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.notifyError(fmt.Errorf("tcp relay read: %w", err))
			return
		}
		if tag[0] != tcpRelayTagDataRecv {
			c.notifyError(fmt.Errorf("tcp relay: unexpected tag 0x%02x", tag[0]))
			return
		}

		// Read sender ID length.
		_ = hdrBuf
		var lenBuf [2]byte
		if _, err := io.ReadFull(relay.conn, lenBuf[:]); err != nil {
			return
		}
		senderLen := int(binary.BigEndian.Uint16(lenBuf[:]))
		if senderLen == 0 || senderLen > 512 {
			c.notifyError(fmt.Errorf("tcp relay: bad sender ID length %d", senderLen))
			return
		}
		senderBytes := make([]byte, senderLen)
		if _, err := io.ReadFull(relay.conn, senderBytes); err != nil {
			return
		}

		// Read payload length.
		var dataLenBuf [4]byte
		if _, err := io.ReadFull(relay.conn, dataLenBuf[:]); err != nil {
			return
		}
		dataLen := int(binary.BigEndian.Uint32(dataLenBuf[:]))
		if dataLen == 0 || dataLen > 1<<20 {
			c.notifyError(fmt.Errorf("tcp relay: bad data length %d", dataLen))
			return
		}
		if dataLen > len(dataBuf) {
			dataBuf = make([]byte, dataLen)
		}
		if _, err := io.ReadFull(relay.conn, dataBuf[:dataLen]); err != nil {
			return
		}

		// Copy payload and dispatch — dataBuf is reused next iteration.
		msg := make([]byte, dataLen)
		copy(msg, dataBuf[:dataLen])
		senderID := string(senderBytes)
		c.maybeActivateRelayForSender(senderID)
		c.processPeerMessage(msg, nil, false)
	}
}

// maybeActivateRelayForSender switches the named peer's Conn to the TCP relay
// path if it isn't already relayed. Called whenever a message arrives from
// that peer via the relay so the return path also uses relay (bidirectional).
func (c *Client) maybeActivateRelayForSender(senderID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	peer, ok := c.peers[senderID]
	if !ok || peer == nil || peer.ViaTURN || c.tcpRelay == nil {
		return
	}
	peer.Conn = &tcpRelayPacketConn{relay: c.tcpRelay, peerID: senderID}
	peer.ViaTURN = true
	peer.LastPeerPong = time.Now()
	fmt.Printf("[TCPRelay] Auto-switched to relay for sender %s\n", senderID[:min(8, len(senderID))])
}

// ConnectViaTCPRelay sets up the TCP relay path for peerID.
// It ensures the shared relay connection exists, then replaces peer.Conn with
// a tcpRelayPacketConn that routes all writes through it.
func (c *Client) ConnectViaTCPRelay(peerID string) error {
	if err := c.connectTCPRelay(); err != nil {
		return fmt.Errorf("tcp relay connect: %w", err)
	}

	c.mutex.Lock()
	peer, ok := c.peers[peerID]
	if !ok || peer == nil {
		c.mutex.Unlock()
		return fmt.Errorf("tcp relay: peer %s not found", peerID)
	}
	if peer.ViaTURN {
		c.mutex.Unlock()
		return nil // already relayed (TURN or TCP)
	}
	relay := c.tcpRelay
	peer.Conn = &tcpRelayPacketConn{relay: relay, peerID: peerID}
	peer.ViaTURN = true // reuse flag: prevents ping/pong from overwriting peer.Address
	peer.LastPeerPong = time.Now()
	c.mutex.Unlock()

	fmt.Printf("[TCPRelay] Relay active for peer %s\n", peerID[:min(8, len(peerID))])
	return nil
}
