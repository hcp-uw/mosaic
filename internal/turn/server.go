package turn

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	serverReadBufSize = 65507
	peerEvictTimeout  = 60 * time.Second
	evictCheckPeriod  = 30 * time.Second
)

// Server is an application-layer relay server. Peers register by peer ID;
// the server forwards data between matched pairs without IP:port permission
// checks. This avoids the symmetric-NAT permission-mismatch bug that makes
// standard TURN unusable when both sides are behind symmetric NAT.
type Server struct {
	conn      *net.UDPConn
	publicIP  string

	mu        sync.RWMutex
	peerAddrs map[string]*net.UDPAddr // peerID → most-recent UDP source address
	sessions  map[string]string       // peerID → desired targetPeerID
	lastSeen  map[string]time.Time    // peerID → last activity time

	ctx    context.Context
	cancel context.CancelFunc
}

// NewServer creates a relay server that listens on addr (e.g. "0.0.0.0:3479").
// publicIP is used only for log output.
func NewServer(addr, publicIP string) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("relay server: resolve addr %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("relay server: listen %s: %w", addr, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		conn:      conn,
		publicIP:  publicIP,
		peerAddrs: make(map[string]*net.UDPAddr),
		sessions:  make(map[string]string),
		lastSeen:  make(map[string]time.Time),
		ctx:       ctx,
		cancel:    cancel,
	}, nil
}

// Start launches the serve loop and eviction goroutine in the background.
func (s *Server) Start() {
	go s.serve()
	go s.evictLoop()
}

// Close shuts down the server.
func (s *Server) Close() {
	s.cancel()
	s.conn.Close()
}

func (s *Server) serve() {
	buf := make([]byte, serverReadBufSize)
	for {
		s.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			fmt.Printf("[RELAY] read error: %v\n", err)
			continue
		}
		if n == 0 {
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handlePacket(pkt, src)
	}
}

func (s *Server) handlePacket(pkt []byte, src *net.UDPAddr) {
	if len(pkt) == 0 {
		return
	}
	switch pkt[0] {
	case OpRelayRegister:
		s.handleRegister(src, pkt[1:])
	case OpRelayConnect:
		s.handleConnect(src, pkt[1:])
	case OpRelayData:
		s.handleData(src, pkt[1:])
	case OpRelayPing:
		s.handlePing(src)
	default:
		// Silently ignore unknown opcodes.
	}
}

func (s *Server) handleRegister(src *net.UDPAddr, payload []byte) {
	peerID, err := DecodeRegister(payload)
	if err != nil {
		fmt.Printf("[RELAY] bad register from %s: %v\n", src, err)
		return
	}
	s.mu.Lock()
	s.peerAddrs[peerID] = src
	s.lastSeen[peerID] = time.Now()
	s.mu.Unlock()
	fmt.Printf("[RELAY] registered %s from %s\n", peerID[:minLen(peerID, 8)], src)
}

func (s *Server) handleConnect(src *net.UDPAddr, payload []byte) {
	myID, targetID, err := DecodeConnect(payload)
	if err != nil {
		fmt.Printf("[RELAY] bad connect from %s: %v\n", src, err)
		return
	}

	s.mu.Lock()
	s.peerAddrs[myID] = src
	s.lastSeen[myID] = time.Now()
	s.sessions[myID] = targetID

	// Check if the target is already waiting for us.
	targetAddr := s.peerAddrs[targetID]
	targetWantsUs := s.sessions[targetID] == myID
	s.mu.Unlock()

	fmt.Printf("[RELAY] connect %s → %s\n", myID[:minLen(myID, 8)], targetID[:minLen(targetID, 8)])

	if targetWantsUs && targetAddr != nil {
		// Both sides are ready — send RelayReady to each.
		s.conn.WriteToUDP(EncodeReady(targetID), src)     //nolint:errcheck
		s.conn.WriteToUDP(EncodeReady(myID), targetAddr)  //nolint:errcheck
		fmt.Printf("[RELAY] session matched %s ↔ %s\n",
			myID[:minLen(myID, 8)], targetID[:minLen(targetID, 8)])
	}
}

func (s *Server) handleData(src *net.UDPAddr, payload []byte) {
	targetID, data, err := DecodeData(payload)
	if err != nil {
		fmt.Printf("[RELAY] bad data from %s: %v\n", src, err)
		return
	}

	s.mu.Lock()
	targetAddr := s.peerAddrs[targetID]
	// Resolve sender's peer ID from address; update it to handle NAT remapping.
	senderID := s.senderIDFromAddr(src)
	if senderID != "" {
		s.peerAddrs[senderID] = src
		s.lastSeen[senderID] = time.Now()
	}
	s.mu.Unlock()

	if targetAddr == nil {
		// Target not yet registered — drop silently; retransmit will retry.
		return
	}

	// Forward with senderID so the receiver knows who sent it.
	s.conn.WriteToUDP(EncodeData(senderID, data), targetAddr) //nolint:errcheck
}

func (s *Server) handlePing(src *net.UDPAddr) {
	s.conn.WriteToUDP(EncodePong(), src) //nolint:errcheck

	// Update last-seen for whoever pinged, even if we can't reverse-lookup.
	s.mu.Lock()
	if id := s.senderIDFromAddr(src); id != "" {
		s.lastSeen[id] = time.Now()
	}
	s.mu.Unlock()
}

// senderIDFromAddr returns the peerID whose registered address matches src.
// Must be called with s.mu held (read or write).
func (s *Server) senderIDFromAddr(src *net.UDPAddr) string {
	srcStr := src.String()
	for id, addr := range s.peerAddrs {
		if addr.String() == srcStr {
			return id
		}
	}
	return ""
}

// evictLoop removes peers with no activity for peerEvictTimeout.
func (s *Server) evictLoop() {
	ticker := time.NewTicker(evictCheckPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-peerEvictTimeout)
			s.mu.Lock()
			for id, ts := range s.lastSeen {
				if ts.Before(cutoff) {
					delete(s.peerAddrs, id)
					delete(s.sessions, id)
					delete(s.lastSeen, id)
					fmt.Printf("[RELAY] evicted idle peer %s\n", id[:minLen(id, 8)])
				}
			}
			s.mu.Unlock()
		}
	}
}

func minLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
