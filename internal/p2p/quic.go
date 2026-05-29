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

	// Receive windows sized for concurrent shard transfers.
	//
	// Redistribution sends up to 14 shards in parallel (one goroutine per shard),
	// each ~10 MB. With the old 8 MB connection window only 2 streams could be
	// in-flight at once — the other 12 blocked on WINDOW_UPDATE, producing the
	// observed sequential asm=1 behaviour.
	//
	// Rule of thumb: InitialConnectionReceiveWindow ≥ N_concurrent × InitialStreamReceiveWindow.
	// 14 shards × 4 MB = 56 MB → round up to 64 MB so all streams flow from the
	// first packet without waiting for auto-tuning to catch up.
	InitialStreamReceiveWindow:     4 * 1024 * 1024,   // 4 MB  per stream
	MaxStreamReceiveWindow:         16 * 1024 * 1024,  // 16 MB per stream
	InitialConnectionReceiveWindow: 64 * 1024 * 1024,  // 64 MB — fits 14+ concurrent streams
	MaxConnectionReceiveWindow:     256 * 1024 * 1024, // 256 MB
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
		// Both sides dialed simultaneously. Use lexicographic ID to pick the
		// canonical connection: the one where the smaller-ID peer is the dialer.
		if dialerID < c.id {
			// Remote (smaller ID) is the canonical dialer — replace our stored conn.
			old := peer.QUICConn
			peer.QUICConn = conn
			c.mutex.Unlock()
			old.CloseWithError(0, "replaced by canonical dialer") //nolint:errcheck
		} else {
			// We have the smaller ID — our outgoing connection is canonical.
			c.mutex.Unlock()
			conn.CloseWithError(0, "duplicate QUIC connection") //nolint:errcheck
			return
		}
	} else {
		peer.QUICConn = conn
		c.mutex.Unlock()
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
		go c.handleQUICStream(peerID, stream)
	}
}

// handleQUICStream reads length-prefixed binary shard frames from a QUIC receive
// stream and delivers each frame to the appropriate handler.
//
// Binary shard frames (magic 0x01) are dispatched SYNCHRONOUSLY via
// callQUICBinaryFrameHandler so that the assembly map is fully updated before the
// stream-done notification fires. All other frames use the async message path.
//
// notifyQUICStreamDone is only called on a CLEAN io.EOF (sender closed the stream
// after the last frame). On connection drop or stream reset the read returns a
// non-EOF error, and we exit without firing the done notification — doing so would
// falsely report all chunks as missing and trigger a 30-second ack-timeout cascade.
//
// Frame format: [4-byte LE length][binary shard chunk frame (0x01 magic)].
func (c *Client) handleQUICStream(peerID string, stream *quic.ReceiveStream) {
	var lastFrame []byte
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(stream, lenBuf[:])
		if err != nil {
			// io.EOF means the sender cleanly closed the stream after the last frame.
			// Any other error (connection drop, reset, partial read) means data may
			// be missing — do NOT fire the done notification in those cases.
			if err == io.EOF && lastFrame != nil {
				c.notifyQUICStreamDone(peerID, lastFrame)
			}
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
		lastFrame = frame
		if len(frame) > 0 && frame[0] == 0x01 {
			c.callQUICBinaryFrameHandler(peerID, frame)
		} else {
			c.notifyMessageReceived(frame)
		}
	}
}

// dialQUICToPeer establishes a QUIC connection to a peer and stores it in
// PeerInfo. Both sides always attempt to dial so that the NAT-side's outgoing
// connection always succeeds regardless of lexicographic ID ordering.
// When both dials succeed (e.g. both peers have public IPs) the duplicate-
// connection guard in dialQUICToPeer and quicConnAccept drops the second one.
func (c *Client) dialQUICToPeer(peerID string) {
	c.mutex.RLock()
	peer := c.peers[peerID]
	qt := c.quicTr
	myID := c.id
	c.mutex.RUnlock()

	if peer == nil || qt == nil || peer.QUICPort == 0 {
		return
	}

	// Skip if we already have a connection (e.g. the peer dialed us first).
	if c.HasQUICConnection(peerID) {
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
	p, ok := c.peers[peerID]
	if !ok {
		c.mutex.Unlock()
		conn.CloseWithError(0, "peer gone") //nolint:errcheck
		return
	}
	if p.QUICConn != nil {
		if myID < peerID {
			// We're the canonical dialer (smaller ID) — replace the existing conn.
			old := p.QUICConn
			p.QUICConn = conn
			c.mutex.Unlock()
			old.CloseWithError(0, "replaced by canonical outgoing") //nolint:errcheck
		} else {
			// Remote is canonical dialer — drop our outgoing connection.
			c.mutex.Unlock()
			conn.CloseWithError(0, "duplicate QUIC connection") //nolint:errcheck
			return
		}
	} else {
		p.QUICConn = conn
		c.mutex.Unlock()
	}

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

// HasQUICConnection reports whether a QUIC connection is established for peerID.
// Used by the upload path to choose the right chunk size without opening a stream.
func (c *Client) HasQUICConnection(peerID string) bool {
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()
	return peer != nil && peer.QUICConn != nil
}

// HasQUICPort reports whether peerID advertised a QUIC port during handshake.
// A true result means the QUIC dial is (or was) in progress; false means QUIC
// was never negotiated and callers should not wait for it.
func (c *Client) HasQUICPort(peerID string) bool {
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()
	return peer != nil && peer.QUICPort > 0
}

// IsPeerViaTURN reports whether the connection to peerID is relayed through TURN.
// Used by the upload path to select a TURN-safe chunk size that avoids IP fragmentation.
func (c *Client) IsPeerViaTURN(peerID string) bool {
	c.mutex.RLock()
	peer := c.peers[peerID]
	c.mutex.RUnlock()
	return peer != nil && peer.ViaTURN
}

