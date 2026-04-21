package stun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

// ClientInfo holds information about connected clients
type ClientInfo struct {
	ID           string
	Address      *net.UDPAddr
	LastPing     time.Time
	Connected    time.Time
	PairedWithID string

	Leader bool
}

// verifyToken calls the auth server's /auth/verify endpoint.
// Returns nil if the token is valid, an error otherwise.
// If authServerURL is empty, verification is skipped (dev mode).
func verifyToken(authServerURL, token string) error {
	if authServerURL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(authServerURL+"/auth/verify", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("could not reach auth server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token rejected by auth server (status %d)", resp.StatusCode)
	}
	return nil
}

// Server represents a STUN server
type Server struct {
	conn          *net.UDPConn
	authServerURL string
	clients       map[string]*ClientInfo
	waitingQueue  []*ClientInfo
	mutex         sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan bool

	currentLeaderID          string
	currentTerm              uint
	leaseExpirationTimeStamp *time.Time
	leaseID                  uint
}

// ServerConfig holds server configuration
type ServerConfig struct {
	ListenAddress string
	AuthServerURL string        // e.g. "http://localhost:8081" — empty disables auth
	ClientTimeout time.Duration
	PingInterval  time.Duration
	MaxQueueSize  int
	EnableLogging bool
}

// DefaultServerConfig returns default server configuration
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ListenAddress: ":3478",
		ClientTimeout: 30 * time.Second,
		PingInterval:  10 * time.Second,
		MaxQueueSize:  100,
		EnableLogging: true,
	}
}

// NewServer creates a new STUN server
func NewServer(config *ServerConfig) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		authServerURL: config.AuthServerURL,
		clients:       make(map[string]*ClientInfo),
		waitingQueue:  make([]*ClientInfo, 0),
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan bool),

		currentLeaderID:          "",
		currentTerm:              0,
		leaseExpirationTimeStamp: nil,
		leaseID:                  0,
	}
}

// Start begins listening for client connections
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

	// Start cleanup routine
	go s.cleanupRoutine(config.ClientTimeout, config.EnableLogging)

	// Start message handling
	go s.handleMessages(config.EnableLogging)

	return nil
}

// Stop stops the server
func (s *Server) Stop() error {
	s.cancel()

	if s.conn != nil {
		s.conn.Close()
	}

	// Wait for cleanup to finish
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		log.Println("Server stop timeout")
	}

	return nil
}

func (s *Server) GetConn() *net.UDPConn {
	return s.conn
}

// handleMessages processes incoming messages from clients
func (s *Server) handleMessages(enableLogging bool) {
	defer func() {
		s.done <- true
	}()

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

// processMessage handles a single message from a client
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

// handleClientRegister handles client registration
func (s *Server) handleClientRegister(msg *api.Message, clientAddr *net.UDPAddr, enableLogging bool) {
	data, err := msg.GetClientRegisterData()
	if err != nil {
		if enableLogging {
			log.Printf("Failed to parse client register data: %v", err)
		}
		s.sendErrorMessage(clientAddr, "Invalid register data", "INVALID_DATA")
		return
	}

	if err := verifyToken(s.authServerURL, data.Token); err != nil {
		if enableLogging {
			log.Printf("Rejected unauthenticated client %s: %v", clientAddr, err)
		}
		s.sendErrorMessage(clientAddr, "Authentication required", "AUTH_REQUIRED")
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Use IP:port as the client ID
	clientID := clientAddr.String()

	clientInfo := &ClientInfo{
		ID:        clientID,
		Address:   clientAddr,
		LastPing:  time.Now(),
		Connected: time.Now(),
	}

	// Check if client already exists
	if existingClient, exists := s.clients[clientID]; exists {
		existingClient.Address = clientAddr
		existingClient.LastPing = time.Now()
		if enableLogging {
			log.Printf("Client %s reconnected", clientID)
		}
		return
	}

	s.clients[clientID] = clientInfo

	if enableLogging {
		log.Printf("Client %s registered", clientID)
	}

	s.sendRegistrationSuccess(clientID, clientAddr)

	if s.currentLeaderID == "" {
		s.sendLeaderAssignment(clientAddr)

		// TODO: Need to perform a check to see if leader is accepted
		s.clients[clientID].Leader = true
		s.currentLeaderID = clientID

	} else {
		s.pairClients(clientInfo, true)
	}
}

func (s *Server) sendRegistrationSuccess(id string, clientAddr *net.UDPAddr) {
	// Currently no specific success message defined
	msg := api.NewRegisterSuccessMessage("Registration successful", id)
	s.sendMessage(clientAddr, msg)
}

// handleClientPing handles ping messages
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

	// Use IP:port as the client ID
	clientID := clientAddr.String()

	if client, exists := s.clients[clientID]; exists {
		client.LastPing = time.Now()
		client.Address = clientAddr
		if enableLogging {
			log.Printf("Ping received from client %s", clientID)
		}
	}
}

// pairClients pairs two clients together
func (s *Server) pairClients(client *ClientInfo, enableLogging bool) {
	// Send peer info to both clients
	s.sendPeerAssignment(s.clients[s.currentLeaderID].Address, client.Address, client.ID)
	s.sendPeerAssignment(client.Address, s.clients[s.currentLeaderID].Address, s.currentLeaderID)

	if enableLogging {
		log.Printf("Paired clients %s to leader %s", client.ID, s.currentLeaderID)
	}

	// Remove both clients from server memory since they no longer need the server
	delete(s.clients, client.ID)

	if enableLogging {
		log.Printf("Removed paired client %s from server memory", client.ID)
	}
}

func (s *Server) sendLeaderAssignment(clientAddr *net.UDPAddr) {
	msg := api.NewServerAssignedLeaderMessage()
	s.sendMessage(clientAddr, msg)
}

// sendPeerAssignment sends peer information to a client
func (s *Server) sendPeerAssignment(clientAddr, peerAddr *net.UDPAddr, peerID string) {
	msg := api.NewPeerAssignmentMessage(peerAddr, peerID)
	s.sendMessage(clientAddr, msg)
}

// sendWaitingMessage sends waiting message to a client
func (s *Server) sendWaitingMessage(clientAddr *net.UDPAddr) {
	msg := api.NewWaitingForPeerMessage()
	s.sendMessage(clientAddr, msg)
}

// sendErrorMessage sends error message to a client
func (s *Server) sendErrorMessage(clientAddr *net.UDPAddr, errorMsg, errorCode string) {
	msg := api.NewServerErrorMessage(errorMsg, errorCode)
	s.sendMessage(clientAddr, msg)
}

// sendMessage sends a message to a client
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

// cleanupRoutine periodically removes inactive clients
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

// cleanupInactiveClients removes clients that haven't pinged recently
func (s *Server) cleanupInactiveClients(timeout time.Duration, enableLogging bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	var toRemove []string

	for clientID, client := range s.clients {
		// Remove inactive clients (paired clients are already removed from memory)
		if now.Sub(client.LastPing) > timeout {
			toRemove = append(toRemove, clientID)
		}
	}

	for _, clientID := range toRemove {
		delete(s.clients, clientID)

		// Remove from waiting queue if present
		for i, waitingClient := range s.waitingQueue {
			if waitingClient.ID == clientID {
				s.waitingQueue = append(s.waitingQueue[:i], s.waitingQueue[i+1:]...)
				break
			}
		}

		if enableLogging {
			log.Printf("Removed inactive client %s", clientID)
		}
	}
}

// GetConnectedClients returns the number of connected clients
func (s *Server) GetConnectedClients() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.clients)
}

// GetWaitingClients returns the number of clients in waiting queue
func (s *Server) GetWaitingClients() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.waitingQueue)
}
