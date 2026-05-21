package p2p

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"

	quic "github.com/quic-go/quic-go"
)

const quicNextProto = "mosaic-p2p"

// serverTLSConfig returns a TLS config with an ephemeral self-signed cert.
// Peer identity is established through the existing X25519/ECDSA mechanisms,
// not through certificate verification.
func serverTLSConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: key}},
		NextProtos:   []string{quicNextProto},
	}, nil
}

// clientTLSConfig returns a TLS config for dialing a peer.
// InsecureSkipVerify is intentional: peer identity is already established via
// the X25519 session handshake on the control channel.
// peerID is set as ServerName so the acceptor can identify the dialer.
func clientTLSConfig(myID string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec — identity verified via X25519 handshake
		NextProtos:         []string{quicNextProto},
		ServerName:         myID, // dialer embeds own ID so acceptor knows who connected
	}
}

var defaultQUICConfig = &quic.Config{
	// 5-minute idle timeout so large-file shard streams survive brief flow-control
	// stalls without the connection being killed mid-transfer.
	// KeepAlivePeriod keeps the connection alive between shard streams.
	MaxIdleTimeout:        5 * time.Minute,
	KeepAlivePeriod:       10 * time.Second,
	MaxIncomingUniStreams: 1000,

	// Large receive windows so the sender is never flow-controlled while the
	// reader goroutine processes frames. Defaults (512 KB stream / 15 MB conn)
	// limit throughput to ~10 MB/s on a 50 ms RTT link; these values raise the
	// ceiling to ~640 MB/s per stream — well above any real-world NIC limit.
	InitialStreamReceiveWindow:     4 * 1024 * 1024,  // 4 MB
	MaxStreamReceiveWindow:         32 * 1024 * 1024, // 32 MB
	InitialConnectionReceiveWindow: 16 * 1024 * 1024, // 16 MB
	MaxConnectionReceiveWindow:     128 * 1024 * 1024, // 128 MB
}

// ──────────────────────────────────────────────────────────
// QUIC connection lifecycle
// ──────────────────────────────────────────────────────────

// quicAcceptLoop accepts incoming QUIC connections from peers.
func (c *Client) quicAcceptLoop() {
	for {
		conn, err := c.quicListener.Accept(c.ctx)
		if err != nil {
			return // listener closed or context cancelled
		}
		go c.quicConnAccept(conn)
	}
}

// quicConnAccept associates an accepted QUIC connection with the correct peer
// (identified by the TLS SNI the dialer set to their own P2P ID).
func (c *Client) quicConnAccept(conn *quic.Conn) {
	dialerID := conn.ConnectionState().TLS.ServerName
	if dialerID == "" {
		conn.CloseWithError(0, "missing peer ID") //nolint:errcheck
		return
	}

	c.mutex.Lock()
	peer, ok := c.peers[dialerID]
	if !ok {
		c.mutex.Unlock()
		conn.CloseWithError(0, "unknown peer") //nolint:errcheck
		return
	}
	if peer.QUICConn != nil {
		// Our own outgoing dial already established a connection — reject
		// this incoming duplicate to avoid leaking the connection.
		c.mutex.Unlock()
		conn.CloseWithError(0, "duplicate QUIC connection") //nolint:errcheck
		return
	}
	peer.QUICConn = conn
	c.mutex.Unlock()
	fmt.Printf("[QUIC] Accepted connection from peer %s\n", dialerID)
	c.quicAcceptStreams(dialerID, conn)
}

// quicAcceptStreams accepts incoming unidirectional streams on a QUIC connection
// and dispatches each to handleQUICStream.
func (c *Client) quicAcceptStreams(peerID string, conn *quic.Conn) {
	for {
		stream, err := conn.AcceptUniStream(c.ctx)
		if err != nil {
			return
		}
		go c.handleQUICStream(stream)
	}
}

// handleQUICStream reads length-prefixed binary shard frames from a QUIC
// receive stream and delivers each frame to the message dispatcher.
// Frame format: [4-byte LE length][binary shard chunk frame (0x01 magic)].
func (c *Client) handleQUICStream(stream *quic.ReceiveStream) {
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
			return
		}
		frameLen := binary.LittleEndian.Uint32(lenBuf[:])
		if frameLen == 0 || frameLen > 16*1024*1024 {
			return // sanity guard
		}
		frame := make([]byte, frameLen)
		if _, err := io.ReadFull(stream, frame); err != nil {
			return
		}
		c.notifyMessageReceivedSync(frame)
	}
}

// dialQUICToPeer establishes a QUIC connection to a peer and stores it in
// PeerInfo. Dial direction is determined by network topology: the node with a
// public IP accepts; the NAT'd node dials outward to the public node's
// reachable QUIC port. This is stable regardless of which node is the current
// leader, so QUIC works correctly even after leadership changes. When both
// nodes have the same reachability (both public or both private/NAT'd), a
// lexicographic tiebreak on peer ID guarantees exactly one dialer.
func (c *Client) dialQUICToPeer(peerID string) {
	c.mutex.RLock()
	peer := c.peers[peerID]
	qt := c.quicTr
	myID := c.id
	c.mutex.RUnlock()

	if peer == nil || qt == nil || peer.QUICPort == 0 {
		return
	}

	myHost, _, _ := net.SplitHostPort(myID)
	mePublic := isPublicIP(net.ParseIP(myHost))
	peerPublic := isPublicIP(peer.Address.IP)

	switch {
	case mePublic && !peerPublic:
		return // we're publicly reachable; let the NAT'd peer dial us
	case mePublic == peerPublic:
		// Both public or both private: lexicographic tiebreak so exactly one side dials.
		if myID > peerID {
			return
		}
	}
	// !mePublic && peerPublic: we're behind NAT, peer is public — we dial (fall through).

	remoteAddr := &net.UDPAddr{
		IP:   peer.Address.IP,
		Port: peer.QUICPort,
	}
	tlsConf := clientTLSConfig(myID)

	ctx := c.ctx
	conn, err := qt.Dial(ctx, remoteAddr, tlsConf, defaultQUICConfig)
	if err != nil {
		fmt.Printf("[QUIC] Dial to peer %s failed: %v\n", peerID, err)
		return
	}

	c.mutex.Lock()
	p, ok := c.peers[peerID]
	if !ok {
		c.mutex.Unlock()
		conn.CloseWithError(0, "peer gone") //nolint:errcheck
		return
	}
	if p.QUICConn != nil {
		// The peer's incoming dial (or our own earlier dial) already set a
		// connection — discard this duplicate rather than leaking it.
		c.mutex.Unlock()
		conn.CloseWithError(0, "duplicate QUIC connection") //nolint:errcheck
		return
	}
	p.QUICConn = conn
	c.mutex.Unlock()

	fmt.Printf("[QUIC] Connected to peer %s\n", peerID)
	c.quicAcceptStreams(peerID, conn)
}

// isPublicIP reports whether ip is a globally routable address.
// Returns false for loopback, link-local, RFC 1918 private, and CGNAT ranges.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	private := []net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
		{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}, // CGNAT
	}
	for _, block := range private {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// OpenShardStream opens a QUIC unidirectional send stream to a peer.
// Returns an error if the QUIC connection for this peer is not yet established,
// or if force-UDP mode is active, in which case callers should fall back to UDP.
func (c *Client) OpenShardStream(peerID string) (io.WriteCloser, error) {
	if c.forceTransport == "udp" {
		return nil, fmt.Errorf("QUIC disabled (force-UDP mode)")
	}
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()

	if peer == nil || peer.QUICConn == nil {
		return nil, fmt.Errorf("no QUIC connection for peer %s", peerID)
	}
	return peer.QUICConn.OpenUniStreamSync(c.ctx)
}

