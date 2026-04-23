package stun

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// ClientInfo holds information about a connected client.
type ClientInfo struct {
	ID            string
	Address       *net.UDPAddr
	LastPing      time.Time
	Connected     time.Time
	QueuePosition int  // server-assigned; 1 = first to join, 2 = second, etc.
	Leader        bool // true when this client is the current leader
}

// Server represents a STUN server.
type Server struct {
	conn *net.UDPConn

	// All registered clients, keyed by IP:port. Clients stay here until they
	// stop pinging — they are NOT removed after pairing.
	clients map[string]*ClientInfo

	// Monotonically increasing counter; each new registration gets the next value.
	queueCounter int

	currentLeaderID string

	mutex sync.Mutex
	ctx   context.Context
	cancel context.CancelFunc
	done  chan bool

	// Legacy fields kept for compatibility.
	waitingQueue             []*ClientInfo
	currentTerm              uint
	leaseExpirationTimeStamp *time.Time
	leaseID                  uint
}

// ServerConfig holds server configuration.
type ServerConfig struct {
	ListenAddress string
	ClientTimeout time.Duration
	PingInterval  time.Duration
	MaxQueueSize  int
	EnableLogging bool
}

// DefaultServerConfig returns a default configuration.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ListenAddress: ":3478",
		ClientTimeout: 30 * time.Second,
		PingInterval:  10 * time.Second,
		MaxQueueSize:  100,
		EnableLogging: true,
	}
}

// NewServer creates a new STUN server.
func NewServer(config *ServerConfig) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		clients:      make(map[string]*ClientInfo),
		waitingQueue:  make([]*ClientInfo, 0),
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan bool),
	}
}

// Start begins listening for UDP messages.
func (s *Server) Start(config *ServerConfig) error {
	addr, err := net.ResolveUDPAddr("udp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP: %w", err)
	}
	s.conn = conn

	if config.EnableLogging {
		log.Printf("STUN server started on %s", config.ListenAddress)
	}

	go s.cleanupRoutine(config.ClientTimeout, config.EnableLogging)
	go s.handleMessages(config.EnableLogging)
	return nil
}

// Stop shuts down the server.
func (s *Server) Stop() error {
	s.cancel()
	if s.conn != nil {
		s.conn.Close()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		log.Println("Server stop timeout")
	}
	return nil
}

func (s *Server) GetConn() *net.UDPConn { return s.conn }

// ──────────────────────────────────────────────────────────────────────────────
// Message loop
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) handleMessages(enableLogging bool) {
	defer func() { s.done <- true }()
	buffer := make([]byte, 1024)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, clientAddr, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if enableLogging {
				log.Printf("Error reading UDP message: %v", err)
			}
			continue
		}
		go s.processMessage(buffer[:n], clientAddr, enableLogging)
	}
}

func (s *Server) processMessage(data []byte, clientAddr *net.UDPAddr, enableLogging bool) {
	msg, err := api.DeserializeMessage(data)
	if err != nil {
		if enableLogging {
			log.Printf("Failed to deserialize message from %s: %v", clientAddr, err)
		}
		s.sendErrorMessage(clientAddr, "Invalid message format", "PARSE_ERROR")
		return
	}

	switch msg.Type {
	case api.ClientRegister:
		s.handleClientRegister(msg, clientAddr, enableLogging)
	case api.ClientPing:
		s.handleClientPing(msg, clientAddr, enableLogging)
	default:
		if enableLogging {
			log.Printf("Unknown message type %s from %s", msg.Type, clientAddr)
		}
		s.sendErrorMessage(clientAddr, "Unknown message type", "UNKNOWN_MESSAGE")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) handleClientRegister(msg *api.Message, clientAddr *net.UDPAddr, enableLogging bool) {
	if _, err := msg.GetClientRegisterData(); err != nil {
		if enableLogging {
			log.Printf("Failed to parse client register data: %v", err)
		}
		s.sendErrorMessage(clientAddr, "Invalid register data", "INVALID_DATA")
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	clientID := clientAddr.String()

	// Reconnecting client — refresh ping time and re-pair if needed.
	if existing, exists := s.clients[clientID]; exists {
		existing.Address = clientAddr
		existing.LastPing = time.Now()
		if enableLogging {
			log.Printf("Client %s reconnected", clientID)
		}
		return
	}

	// Assign next queue position.
	s.queueCounter++
	queuePos := s.queueCounter

	clientInfo := &ClientInfo{
		ID:            clientID,
		Address:       clientAddr,
		LastPing:      time.Now(),
		Connected:     time.Now(),
		QueuePosition: queuePos,
	}
	s.clients[clientID] = clientInfo

	if enableLogging {
		log.Printf("Client %s registered (queue position %d)", clientID, queuePos)
	}

	s.sendRegistrationSuccess(clientID, clientAddr, queuePos)

	if s.currentLeaderID == "" {
		s.promoteAsLeader(clientInfo, enableLogging)
	} else {
		s.pairWithLeader(clientInfo, enableLogging)
	}
}

// promoteAsLeader designates a client as the network leader.
// Must be called with s.mutex held.
func (s *Server) promoteAsLeader(client *ClientInfo, enableLogging bool) {
	client.Leader = true
	s.currentLeaderID = client.ID
	s.sendLeaderAssignment(client.Address)
	if enableLogging {
		log.Printf("Client %s assigned as leader (queue position %d)", client.ID, client.QueuePosition)
	}
}

// pairWithLeader sends peer-assignment messages to both the leader and the new client.
// Must be called with s.mutex held.
func (s *Server) pairWithLeader(client *ClientInfo, enableLogging bool) {
	leader, exists := s.clients[s.currentLeaderID]
	if !exists || leader == nil {
		// Leader gone — promote this client instead.
		if enableLogging {
			log.Printf("Leader gone when pairing %s — promoting as new leader", client.ID)
		}
		s.promoteAsLeader(client, enableLogging)
		return
	}

	s.sendPeerAssignment(leader.Address, client.Address, client.ID)
	s.sendPeerAssignment(client.Address, leader.Address, s.currentLeaderID)

	if enableLogging {
		log.Printf("Paired client %s (queue %d) with leader %s (queue %d)",
			client.ID, client.QueuePosition, leader.ID, leader.QueuePosition)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Ping
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) handleClientPing(msg *api.Message, clientAddr *net.UDPAddr, enableLogging bool) {
	_, err := msg.GetClientRegisterData()
	if err != nil {
		if enableLogging {
			log.Printf("Failed to parse ping data: %v", err)
		}
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	clientID := clientAddr.String()
	if client, exists := s.clients[clientID]; exists {
		client.LastPing = time.Now()
		client.Address = clientAddr
		if enableLogging {
			log.Printf("Ping received from client %s", clientID)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Leader election
// ──────────────────────────────────────────────────────────────────────────────

// electNewLeader picks the active client with the lowest queue position and
// promotes them as leader, then re-pairs all other active clients with them.
// Must be called with s.mutex held.
func (s *Server) electNewLeader(enableLogging bool) {
	// Collect all remaining clients sorted by queue position (ascending).
	candidates := make([]*ClientInfo, 0, len(s.clients))
	for _, c := range s.clients {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].QueuePosition < candidates[j].QueuePosition
	})

	if len(candidates) == 0 {
		s.currentLeaderID = ""
		if enableLogging {
			log.Println("No clients remaining after leader departure.")
		}
		return
	}

	newLeader := candidates[0]
	s.promoteAsLeader(newLeader, enableLogging)

	// Re-pair all other clients with the new leader.
	for _, c := range candidates[1:] {
		c.Leader = false
		s.sendPeerAssignment(newLeader.Address, c.Address, c.ID)
		s.sendPeerAssignment(c.Address, newLeader.Address, newLeader.ID)
		if enableLogging {
			log.Printf("Re-paired %s with new leader %s", c.ID, newLeader.ID)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Cleanup
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) cleanupRoutine(timeout time.Duration, enableLogging bool) {
	ticker := time.NewTicker(timeout / 2)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cleanupInactiveClients(timeout, enableLogging)
		}
	}
}

func (s *Server) cleanupInactiveClients(timeout time.Duration, enableLogging bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	leaderRemoved := false

	for clientID, client := range s.clients {
		if now.Sub(client.LastPing) > timeout {
			if client.Leader {
				leaderRemoved = true
			}
			delete(s.clients, clientID)
			if enableLogging {
				log.Printf("Removed inactive client %s (queue %d, leader=%v)",
					clientID, client.QueuePosition, client.Leader)
			}
		}
	}

	if leaderRemoved {
		s.currentLeaderID = ""
		if enableLogging {
			log.Println("Leader removed — electing new leader.")
		}
		s.electNewLeader(enableLogging)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Messaging helpers
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) sendRegistrationSuccess(id string, clientAddr *net.UDPAddr, queuePos int) {
	msg := api.NewRegisterSuccessMessage("Registration successful", id, queuePos)
	s.sendMessage(clientAddr, msg)
}

func (s *Server) sendLeaderAssignment(clientAddr *net.UDPAddr) {
	msg := api.NewServerAssignedLeaderMessage()
	s.sendMessage(clientAddr, msg)
}

func (s *Server) sendPeerAssignment(clientAddr, peerAddr *net.UDPAddr, peerID string) {
	msg := api.NewPeerAssignmentMessage(peerAddr, peerID)
	s.sendMessage(clientAddr, msg)
}

func (s *Server) sendErrorMessage(clientAddr *net.UDPAddr, errorMsg, errorCode string) {
	msg := api.NewServerErrorMessage(errorMsg, errorCode)
	s.sendMessage(clientAddr, msg)
}

func (s *Server) sendMessage(clientAddr *net.UDPAddr, msg *api.Message) {
	data, err := msg.Serialize()
	if err != nil {
		log.Printf("Failed to serialize message: %v", err)
		return
	}
	_, err = s.conn.WriteToUDP(data, clientAddr)
	if err != nil {
		log.Printf("Failed to send message to %s: %v", clientAddr, err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Stats
// ──────────────────────────────────────────────────────────────────────────────

func (s *Server) GetConnectedClients() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.clients)
}

func (s *Server) GetWaitingClients() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.waitingQueue)
}
