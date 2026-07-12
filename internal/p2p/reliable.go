package p2p

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/hcp-uw/mosaic/internal/api"
)

// maxReliableUDPPayload is the largest message we send in a single UDP datagram.
// A UDP payload tops out at 65507 bytes; we stay under it with margin. Anything
// larger (a big network manifest) goes over a size-unbounded reliable transport.
const maxReliableUDPPayload = 60000

// SendReliableToPeer sends msg to a peer over a transport that can carry it
// regardless of size — used for the network manifest, which grows with the
// network and would otherwise fail once it exceeds one UDP datagram (~65 KB).
//
// The message is session-encrypted end-to-end first, exactly like the normal
// path, so the receiver decrypts and authenticates it identically no matter which
// transport carried it. Small messages take the peer's normal connection. A
// message too big for a datagram is sent over QUIC (preferred — direct, reliable,
// unbounded) or, if no QUIC connection exists, the TCP relay (reliable, up to
// 1 MB). Both receive paths already run through processPeerMessage, so nothing
// on the receiving side needs to special-case the transport.
func (c *Client) SendReliableToPeer(peerID string, msg *api.Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return err
	}

	c.mutex.RLock()
	peer := c.peers[peerID]
	relay := c.tcpRelay
	c.mutex.RUnlock()
	if peer == nil || peer.Conn == nil {
		return fmt.Errorf("peer %s not connected", peerID)
	}

	// Session-encrypt once (the 0x02 envelope the receiver expects). Manifests are
	// only sent after the handshake, so HandshakeDone is expected to be true.
	if peer.HandshakeDone {
		enc, serr := peer.sealForPeer(data)
		if serr != nil {
			return fmt.Errorf("session encrypt failed: %w", serr)
		}
		data = enc
	}

	// Small enough for one UDP datagram → the peer's normal transport handles it
	// (direct UDP, TURN relay, or TCP relay — all fine at this size).
	if len(data) <= maxReliableUDPPayload {
		_, werr := peer.Conn.WriteTo(data, peer.Address)
		return werr
	}

	// Too big for a datagram. Prefer QUIC: direct, reliable, no size limit.
	if c.HasQUICConnection(peerID) {
		if stream, serr := c.OpenShardStream(peerID); serr == nil {
			defer stream.Close()
			return writeQUICFrame(stream, data)
		}
	}
	// Fall back to the TCP relay: reliable, up to 1 MB, always eagerly connected.
	if relay != nil {
		return relay.sendTo(peerID, data)
	}
	// No QUIC and no relay — best-effort over the normal conn. This only succeeds
	// if that conn is itself the TCP relay (which tolerates ~1 MB); a large message
	// over direct UDP or TURN will fail here, and the periodic full-sync retries.
	_, werr := peer.Conn.WriteTo(data, peer.Address)
	return werr
}

// writeQUICFrame writes a length-prefixed frame to a QUIC send stream, using the
// same 4-byte little-endian framing that handleQUICStream reads. (Kept in the p2p
// package to avoid an import cycle with transfer, which has an identical helper.)
func writeQUICFrame(w io.WriteCloser, frame []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(frame)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(frame)
	return err
}
