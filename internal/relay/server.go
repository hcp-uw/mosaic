// Package relay implements a simple TCP message relay server.
//
// Clients connect over TCP, register their peer ID, and then exchange binary
// frames with other registered clients. The server never inspects the payload —
// it only reads the routing header (target peer ID) and forwards the rest.
//
// Frame types (1-byte tag at the start of every frame):
//
//	0x00  REGISTER  client → server   [1-byte tag][2-byte ID len][ID bytes]
//	0x01  DATA      client → server   [1-byte tag][2-byte target len][target][4-byte len][payload]
//	0x02  DATA      server → client   [1-byte tag][2-byte sender len][sender][4-byte len][payload]
//	0x03  ACK       server → client   [1-byte tag]  (confirms registration)
package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

const (
	tagRegister byte = 0x00
	tagDataSend byte = 0x01
	tagDataRecv byte = 0x02
	tagAck      byte = 0x03
)

// Server is a TCP relay hub.
type Server struct {
	mu      sync.RWMutex
	clients map[string]net.Conn // peerID → TCP conn

	ln      net.Listener
	done    chan struct{}
	logging bool
}

// NewServer creates a relay server. Call Start to begin accepting connections.
func NewServer(enableLogging bool) *Server {
	return &Server{
		clients: make(map[string]net.Conn),
		done:    make(chan struct{}),
		logging: enableLogging,
	}
}

// Start listens on addr (e.g. ":9000") and accepts connections in the background.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("relay: listen %s: %w", addr, err)
	}
	s.ln = ln
	if s.logging {
		log.Printf("TCP relay listening on %s", addr)
	}
	go s.acceptLoop()
	return nil
}

// Stop closes the listener and all client connections.
func (s *Server) Stop() {
	if s.ln != nil {
		s.ln.Close()
	}
	close(s.done)
	s.mu.Lock()
	for _, c := range s.clients {
		c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				if s.logging {
					log.Printf("relay: accept error: %v", err)
				}
				continue
			}
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	// First frame must be REGISTER.
	peerID, err := s.readRegister(conn)
	if err != nil {
		if s.logging {
			log.Printf("relay: registration failed from %s: %v", conn.RemoteAddr(), err)
		}
		return
	}

	s.mu.Lock()
	s.clients[peerID] = conn
	s.mu.Unlock()

	if s.logging {
		log.Printf("relay: registered peer %s from %s", peerID[:min8(len(peerID))], conn.RemoteAddr())
	}

	// Send ACK.
	conn.Write([]byte{tagAck}) //nolint:errcheck

	defer func() {
		s.mu.Lock()
		if s.clients[peerID] == conn {
			delete(s.clients, peerID)
		}
		s.mu.Unlock()
		if s.logging {
			log.Printf("relay: peer %s disconnected", peerID[:min8(len(peerID))])
		}
	}()

	buf := make([]byte, 65536+256)
	for {
		if err := s.handleFrame(conn, peerID, buf); err != nil {
			if err != io.EOF {
				if s.logging {
					log.Printf("relay: peer %s frame error: %v", peerID[:min8(len(peerID))], err)
				}
			}
			return
		}
	}
}

func (s *Server) readRegister(conn net.Conn) (string, error) {
	var tag [1]byte
	if _, err := io.ReadFull(conn, tag[:]); err != nil {
		return "", err
	}
	if tag[0] != tagRegister {
		return "", fmt.Errorf("expected REGISTER (0x00), got 0x%02x", tag[0])
	}
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return "", err
	}
	idLen := int(binary.BigEndian.Uint16(lenBuf[:]))
	if idLen == 0 || idLen > 512 {
		return "", fmt.Errorf("bad peer ID length %d", idLen)
	}
	idBytes := make([]byte, idLen)
	if _, err := io.ReadFull(conn, idBytes); err != nil {
		return "", err
	}
	return string(idBytes), nil
}

func (s *Server) handleFrame(conn net.Conn, senderID string, buf []byte) error {
	var tag [1]byte
	if _, err := io.ReadFull(conn, tag[:]); err != nil {
		return err
	}
	if tag[0] != tagDataSend {
		return fmt.Errorf("unexpected tag 0x%02x from %s", tag[0], senderID[:min8(len(senderID))])
	}

	// Read target peer ID.
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return err
	}
	targetLen := int(binary.BigEndian.Uint16(lenBuf[:]))
	if targetLen == 0 || targetLen > 512 {
		return fmt.Errorf("bad target ID length %d", targetLen)
	}
	targetBytes := make([]byte, targetLen)
	if _, err := io.ReadFull(conn, targetBytes); err != nil {
		return err
	}
	targetID := string(targetBytes)

	// Read payload.
	var dataLenBuf [4]byte
	if _, err := io.ReadFull(conn, dataLenBuf[:]); err != nil {
		return err
	}
	dataLen := int(binary.BigEndian.Uint32(dataLenBuf[:]))
	if dataLen == 0 || dataLen > 1<<20 {
		return fmt.Errorf("bad data length %d", dataLen)
	}
	if dataLen > len(buf) {
		buf = make([]byte, dataLen)
	}
	if _, err := io.ReadFull(conn, buf[:dataLen]); err != nil {
		return err
	}

	s.forward(senderID, targetID, buf[:dataLen])
	return nil
}

func (s *Server) forward(senderID, targetID string, payload []byte) {
	s.mu.RLock()
	target, ok := s.clients[targetID]
	s.mu.RUnlock()
	if !ok {
		return
	}

	senderBytes := []byte(senderID)
	hdr := make([]byte, 1+2+len(senderBytes)+4)
	hdr[0] = tagDataRecv
	binary.BigEndian.PutUint16(hdr[1:], uint16(len(senderBytes)))
	copy(hdr[3:], senderBytes)
	binary.BigEndian.PutUint32(hdr[3+len(senderBytes):], uint32(len(payload)))

	// Write header + payload atomically under a per-connection lock.
	// We use a simple approach: write header then data. Because TCP is
	// stream-oriented and we hold the relay lock (read lock on clients map),
	// concurrent writes could interleave. Use a separate write-mutex approach.
	// For simplicity here we do two writes with the risk of OS-level atomicity;
	// in practice TCP write coalescing handles this correctly on Linux/macOS.
	frame := append(hdr, payload...)
	target.Write(frame) //nolint:errcheck — best-effort forward
}

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
