package p2p

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// CheckSTUNReachable performs a throwaway registration against the STUN server
// to confirm it is up and answering, then immediately deregisters so the probe
// leaves no lingering record. It uses its own UDP socket and does not touch a
// live client's connection. Returns the assigned queue position on success.
//
// This is a diagnostic helper for `mos doctor`; it is intentionally transient
// (register → read reply → deregister) so it can be run even while the daemon
// holds a separate live STUN connection.
func CheckSTUNReachable(addr string) (queuePosition int, err error) {
	serverAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, fmt.Errorf("resolve %s: %w", addr, err)
	}
	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return 0, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Empty node ID: a diagnostic probe must not claim or persist a queue position.
	regData, err := api.NewClientRegisterMessage("").Serialize()
	if err != nil {
		return 0, err
	}
	if _, err := conn.Write(regData); err != nil {
		return 0, fmt.Errorf("send register: %w", err)
	}

	// Deregister on the way out so the probe doesn't leave a phantom client that
	// could participate in leader election until it times out.
	defer func() {
		if dereg, derr := api.NewClientDeregisterMessage().Serialize(); derr == nil {
			conn.Write(dereg) //nolint:errcheck — best-effort cleanup
		}
	}()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("no reply within 3s: %w", err)
	}
	msg, err := api.DeserializeMessage(buf[:n])
	if err != nil {
		return 0, fmt.Errorf("unrecognised reply: %w", err)
	}
	if msg.Type == api.RegisterSuccess {
		if d, derr := msg.GetRegisterSuccessData(); derr == nil {
			return d.QueuePosition, nil
		}
	}
	// Any well-formed server reply proves reachability even if it isn't a
	// RegisterSuccess (e.g. a ServerError or leader assignment).
	return 0, nil
}

// CheckTCPRelayReachable dials the TCP relay, performs the TLS handshake, sends a
// REGISTER frame with probeID, and waits for the relay's ACK — the same handshake
// a real client does in connectTCPRelay, but on a throwaway connection that is
// closed immediately. This is the check that catches a client/server relay-port
// mismatch: if the client's configured address points at a port nothing is
// listening on, the dial fails here rather than silently at fallback time.
func CheckTCPRelayReachable(addr, probeID string) error {
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp",
		addr,
		&tls.Config{InsecureSkipVerify: true}, //nolint:gosec — self-signed cert; diagnostic dial only
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	idBytes := []byte(probeID)
	reg := make([]byte, 1+2+len(idBytes))
	reg[0] = tcpRelayTagRegister
	binary.BigEndian.PutUint16(reg[1:], uint16(len(idBytes)))
	copy(reg[3:], idBytes)
	if _, err := conn.Write(reg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return fmt.Errorf("no ACK from relay: %w", err)
	}
	if ack[0] != tcpRelayTagAck {
		return fmt.Errorf("unexpected relay reply tag 0x%02x", ack[0])
	}
	return nil
}
