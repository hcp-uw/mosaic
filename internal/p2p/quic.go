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
	MaxIdleTimeout:        30 * time.Second,
	KeepAlivePeriod:       10 * time.Second,
	MaxIncomingUniStreams: 1000,
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
	if ok {
		peer.QUICConn = conn
	}
	c.mutex.Unlock()

	if !ok {
		conn.CloseWithError(0, "unknown peer") //nolint:errcheck
		return
	}
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
		c.notifyMessageReceived(frame)
	}
}

// dialQUICToPeer establishes a QUIC connection to a peer and stores it in
// PeerInfo. Called only by the side with the lexicographically smaller P2P ID
// to avoid duplicate connections.
func (c *Client) dialQUICToPeer(peerID string) {
	c.mutex.RLock()
	peer := c.peers[peerID]
	qt := c.quicTr
	myID := c.id
	c.mutex.RUnlock()

	if peer == nil || qt == nil || peer.QUICPort == 0 {
		return
	}

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
	if p, ok := c.peers[peerID]; ok {
		p.QUICConn = conn
	}
	c.mutex.Unlock()

	fmt.Printf("[QUIC] Connected to peer %s\n", peerID)
	c.quicAcceptStreams(peerID, conn)
}

// OpenShardStream opens a QUIC unidirectional send stream to a peer.
// Returns an error if the QUIC connection for this peer is not yet established,
// in which case callers should fall back to UDP.
func (c *Client) OpenShardStream(peerID string) (io.WriteCloser, error) {
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()

	if peer == nil || peer.QUICConn == nil {
		return nil, fmt.Errorf("no QUIC connection for peer %s", peerID)
	}
	return peer.QUICConn.OpenUniStreamSync(c.ctx)
}

