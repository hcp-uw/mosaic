package transport

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// MockUDPConn provides a mock implementation of UDPConnInterface for testing
type MockUDPConn struct {
	mu              sync.Mutex
	writeData       [][]byte      // Tracks all data written to this connection
	readData        [][]byte      // Data to return on ReadFromUDP calls
	readIndex       int           // Current index in readData
	readDeadline    time.Time     // Current read deadline
	writeError      error         // Error to return on Write calls
	readError       error         // Error to return on ReadFromUDP calls
	setDeadlineErr  error         // Error to return on SetReadDeadline calls
	closeError      error         // Error to return on Close calls
	closed          bool          // Whether the connection has been closed
	addr            *net.UDPAddr  // Remote address to return
	writeDelay      time.Duration // Delay before Write returns
	readDelay       time.Duration // Delay before ReadFromUDP returns
	localAddr       *net.UDPAddr  // Local address for this connection
	hubEnabled      bool          // Whether to use the hub for message routing
}

// Ensure MockUDPConn implements UDPConnInterface
var _ UDPConnInterface = (*MockUDPConn)(nil)

// NewMockUDPConn creates a new mock UDP connection
func NewMockUDPConn(remoteAddr string) *MockUDPConn {
	addr, _ := net.ResolveUDPAddr("udp", remoteAddr)
	return &MockUDPConn{
		addr:       addr,
		writeData:  make([][]byte, 0),
		readData:   make([][]byte, 0),
		hubEnabled: false,
	}
}

// NewConnectedMockUDPConn creates a mock UDP connection that uses the hub for communication
func NewConnectedMockUDPConn(localAddr, remoteAddr string) *MockUDPConn {
	lAddr, _ := net.ResolveUDPAddr("udp", localAddr)
	rAddr, _ := net.ResolveUDPAddr("udp", remoteAddr)
	
	conn := &MockUDPConn{
		addr:       rAddr,
		localAddr:  lAddr,
		writeData:  make([][]byte, 0),
		readData:   make([][]byte, 0),
		hubEnabled: true,
	}
	
	// Register with the global hub
	globalMockHub.RegisterConnection(localAddr, conn)
	
	return conn
}

// Write implements UDPConnInterface
func (m *MockUDPConn) Write(data []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, fmt.Errorf("connection closed")
	}

	// Simulate write delay
	if m.writeDelay > 0 {
		time.Sleep(m.writeDelay)
	}

	if m.writeError != nil {
		return 0, m.writeError
	}

	// Store a copy of the written data
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	m.writeData = append(m.writeData, dataCopy)

	// If hub is enabled, route the message to the destination
	if m.hubEnabled && m.addr != nil {
		// Route message to the remote address
		if err := globalMockHub.RouteMessage(m.addr.String(), dataCopy); err != nil {
			// If routing fails, that's OK - might be testing scenarios where peer is not available
		}
	}

	return len(data), nil
}

// ReadFromUDP implements UDPConnInterface
func (m *MockUDPConn) ReadFromUDP(buffer []byte) (int, *net.UDPAddr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, nil, fmt.Errorf("connection closed")
	}

	// Simulate read delay
	if m.readDelay > 0 {
		time.Sleep(m.readDelay)
	}

	if m.readError != nil {
		return 0, nil, m.readError
	}

	// Check read deadline for immediate timeout
	if !m.readDeadline.IsZero() && time.Now().After(m.readDeadline) {
		return 0, nil, &net.OpError{
			Op:  "read",
			Net: "udp",
			Err: &timeoutError{},
		}
	}

	// Return mock data if available
	if m.readIndex < len(m.readData) {
		data := m.readData[m.readIndex]
		m.readIndex++

		n := copy(buffer, data)
		return n, m.addr, nil
	}

	// No data available - simulate timeout if deadline is set
	if !m.readDeadline.IsZero() {
		// Wait until deadline, then return timeout
		sleepDuration := time.Until(m.readDeadline)
		if sleepDuration > 0 {
			time.Sleep(sleepDuration)
		}

		return 0, nil, &net.OpError{
			Op:  "read",
			Net: "udp",
			Err: &timeoutError{},
		}
	}

	// Otherwise block (in real scenarios, this would be handled by the test timeout)
	return 0, nil, fmt.Errorf("no data available")
}

// SetReadDeadline implements UDPConnInterface
func (m *MockUDPConn) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setDeadlineErr != nil {
		return m.setDeadlineErr
	}

	m.readDeadline = t
	return nil
}

// Close implements UDPConnInterface
func (m *MockUDPConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closeError != nil {
		return m.closeError
	}

	// Unregister from hub if connected
	if m.hubEnabled && m.localAddr != nil {
		globalMockHub.UnregisterConnection(m.localAddr.String())
	}

	m.closed = true
	return nil
}

// Test helper methods

// GetWrittenData returns all data written to this connection
func (m *MockUDPConn) GetWrittenData() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return a copy to prevent race conditions
	result := make([][]byte, len(m.writeData))
	for i, data := range m.writeData {
		result[i] = make([]byte, len(data))
		copy(result[i], data)
	}
	return result
}

// AddReadData adds data that will be returned by future ReadFromUDP calls
func (m *MockUDPConn) AddReadData(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	m.readData = append(m.readData, dataCopy)
}

// SetWriteError configures the connection to return an error on Write calls
func (m *MockUDPConn) SetWriteError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeError = err
}

// SetReadError configures the connection to return an error on ReadFromUDP calls
func (m *MockUDPConn) SetReadError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readError = err
}

// SetWriteDelay configures a delay before Write operations complete
func (m *MockUDPConn) SetWriteDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeDelay = delay
}

// SetReadDelay configures a delay before ReadFromUDP operations complete
func (m *MockUDPConn) SetReadDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readDelay = delay
}

// Reset clears all recorded data and errors
func (m *MockUDPConn) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.writeData = m.writeData[:0]
	m.readData = m.readData[:0]
	m.readIndex = 0
	m.writeError = nil
	m.readError = nil
	m.setDeadlineErr = nil
	m.closeError = nil
	m.closed = false
	m.readDeadline = time.Time{}
}

// timeoutError implements net.Error for timeout simulation
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// MockUDPDialer provides a mock implementation of UDPDialerInterface for testing
type MockUDPDialer struct {
	mu          sync.Mutex
	connections map[string]*MockUDPConn // Map of address -> mock connection
	dialError   error                   // Error to return on DialUDP calls
	hubEnabled  bool                    // Whether to create hub-connected connections
	localAddrCounter int                // Counter for generating unique local addresses
}

// Ensure MockUDPDialer implements UDPDialerInterface
var _ UDPDialerInterface = (*MockUDPDialer)(nil)

// NewMockUDPDialer creates a new mock UDP dialer
func NewMockUDPDialer() *MockUDPDialer {
	return &MockUDPDialer{
		connections: make(map[string]*MockUDPConn),
		hubEnabled: false,
	}
}

// NewConnectedMockUDPDialer creates a mock UDP dialer that creates hub-connected connections
func NewConnectedMockUDPDialer() *MockUDPDialer {
	return &MockUDPDialer{
		connections: make(map[string]*MockUDPConn),
		hubEnabled: true,
	}
}

// DialUDP implements UDPDialerInterface
func (d *MockUDPDialer) DialUDP(network string, laddr, raddr *net.UDPAddr) (UDPConnInterface, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.dialError != nil {
		return nil, d.dialError
	}

	addr := raddr.String()

	// Return existing connection or create a new one
	if conn, exists := d.connections[addr]; exists {
		return conn, nil
	}

	var conn *MockUDPConn
	if d.hubEnabled {
		// Generate a unique local address
		d.localAddrCounter++
		localAddr := fmt.Sprintf("127.0.0.1:%d", 50000+d.localAddrCounter)
		conn = NewConnectedMockUDPConn(localAddr, addr)
	} else {
		conn = NewMockUDPConn(addr)
	}

	d.connections[addr] = conn
	return conn, nil
}

// GetConnection returns the mock connection for a given address
func (d *MockUDPDialer) GetConnection(addr string) *MockUDPConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connections[addr]
}

// SetDialError configures the dialer to return an error on DialUDP calls
func (d *MockUDPDialer) SetDialError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dialError = err
}

// Reset clears all connections and errors
func (d *MockUDPDialer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connections = make(map[string]*MockUDPConn)
	d.dialError = nil
	d.localAddrCounter = 0
	
	// Reset the global hub if we're using connected mode
	if d.hubEnabled {
		globalMockHub.Reset()
	}
}

// MockNetworkHub enables communication between mock UDP connections
type MockNetworkHub struct {
	mu          sync.Mutex
	connections map[string]*MockUDPConn // address -> connection mapping
}

// Global hub for coordinating mock connections
var globalMockHub = &MockNetworkHub{
	connections: make(map[string]*MockUDPConn),
}

// RegisterConnection adds a connection to the hub
func (h *MockNetworkHub) RegisterConnection(addr string, conn *MockUDPConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connections[addr] = conn
}

// UnregisterConnection removes a connection from the hub
func (h *MockNetworkHub) UnregisterConnection(addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.connections, addr)
}

// RouteMessage routes a message from srcAddr to destAddr
func (h *MockNetworkHub) RouteMessage(destAddr string, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	destConn, exists := h.connections[destAddr]
	if !exists {
		return fmt.Errorf("destination connection %s not found", destAddr)
	}
	
	// Add the data to the destination connection's read buffer
	destConn.AddReadData(data)
	return nil
}

// GetConnection returns a connection by address
func (h *MockNetworkHub) GetConnection(addr string) *MockUDPConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connections[addr]
}

// Reset clears all registered connections
func (h *MockNetworkHub) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connections = make(map[string]*MockUDPConn)
}

// GetGlobalMockHub returns the global mock hub for testing
func GetGlobalMockHub() *MockNetworkHub {
	return globalMockHub
}

// MockPeer represents a mock peer with realistic protocol message handlers
type MockPeer struct {
	Address    string
	PrivateKey []byte // ed25519.PrivateKey stored as bytes
	PublicKey  []byte // ed25519.PublicKey stored as bytes
	CRDT       interface{} // Mock CRDT data
	PeerIPs    [][]byte    // List of peer IPs as NodeIP (net.IP)
}

// NewMockPeer creates a new mock peer and registers it with the hub
func NewMockPeer(address string, privateKey, publicKey []byte) *MockPeer {
	peer := &MockPeer{
		Address:    address,
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		CRDT:       createMockCRDT(),
		PeerIPs:    make([][]byte, 0),
	}
	
	// Create a connection for this peer and register with hub
	conn := NewConnectedMockUDPConn(address, address) // Use same address for local/remote
	
	// Set up message handler that responds to protocol messages
	go peer.handleMessages(conn)
	
	return peer
}

// handleMessages processes incoming messages and generates appropriate responses
func (p *MockPeer) handleMessages(conn *MockUDPConn) {
	// This is a simplified message handler - in a real test we'd want more sophisticated routing
	// For now, we'll add responses manually to the connection when needed
}

// createMockCRDT creates a simple mock CRDT for testing
func createMockCRDT() interface{} {
	return map[string]interface{}{
		"join_messages": make(map[string]interface{}),
		"file_manifest": make(map[string]interface{}),
	}
}