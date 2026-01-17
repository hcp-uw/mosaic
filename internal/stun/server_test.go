package stun

import (
	"net"
	"testing"
	"time"
)

func TestServerStartStop(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0", // Use random port
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)

	// Test start
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test stop
	err = server.Stop()
	if err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}
}

func TestClientRegistration(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Get the actual listening address
	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	// Create a test client connection
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer clientConn.Close()

	// Send registration message
	registerMsg := NewClientRegisterMessage()
	data, err := registerMsg.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize message: %v", err)
	}

	_, err = clientConn.Write(data)
	if err != nil {
		t.Fatalf("Failed to send registration: %v", err)
	}

	// Read response
	buffer := make([]byte, 1024)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buffer)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	responseMsg, err := DeserializeMessage(buffer[:n])
	if err != nil {
		t.Fatalf("Failed to deserialize response: %v", err)
	}

	// Should receive waiting message
	if responseMsg.Type != WaitingForPeer {
		t.Errorf("Expected waiting message, got: %v", responseMsg.Type)
	}

	// Check server state
	if server.GetConnectedClients() != 1 {
		t.Errorf("Expected 1 connected client, got: %d", server.GetConnectedClients())
	}

	if server.GetWaitingClients() != 1 {
		t.Errorf("Expected 1 waiting client, got: %d", server.GetWaitingClients())
	}
}

func TestClientPairing(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	// Connect first client
	client1Conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect client 1: %v", err)
	}
	defer client1Conn.Close()

	// Connect second client
	client2Conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect client 2: %v", err)
	}
	defer client2Conn.Close()

	// Register first client
	registerMsg1 := NewClientRegisterMessage()
	data1, _ := registerMsg1.Serialize()
	client1Conn.Write(data1)

	// Read first client response (should be waiting)
	buffer1 := make([]byte, 1024)
	client1Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n1, err := client1Conn.Read(buffer1)
	if err != nil {
		t.Fatalf("Failed to read client 1 response: %v", err)
	}

	responseMsg1, _ := DeserializeMessage(buffer1[:n1])
	if responseMsg1.Type != WaitingForPeer {
		t.Errorf("Expected waiting message for client 1, got: %v", responseMsg1.Type)
	}

	// Register second client
	registerMsg2 := NewClientRegisterMessage()
	data2, _ := registerMsg2.Serialize()
	client2Conn.Write(data2)

	// Both clients should receive peer assignment messages
	client1Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	client2Conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Read client 1 peer assignment
	n1, err = client1Conn.Read(buffer1)
	if err != nil {
		t.Fatalf("Failed to read client 1 peer assignment: %v", err)
	}

	peerMsg1, err := DeserializeMessage(buffer1[:n1])
	if err != nil {
		t.Fatalf("Failed to deserialize client 1 peer message: %v", err)
	}

	if peerMsg1.Type != PeerAssignment {
		t.Errorf("Expected peer assignment for client 1, got: %v", peerMsg1.Type)
	}

	// Read client 2 peer assignment
	buffer2 := make([]byte, 1024)
	n2, err := client2Conn.Read(buffer2)
	if err != nil {
		t.Fatalf("Failed to read client 2 peer assignment: %v", err)
	}

	peerMsg2, err := DeserializeMessage(buffer2[:n2])
	if err != nil {
		t.Fatalf("Failed to deserialize client 2 peer message: %v", err)
	}

	if peerMsg2.Type != PeerAssignment {
		t.Errorf("Expected peer assignment for client 2, got: %v", peerMsg2.Type)
	}

	// Verify peer information - peer IDs are now IP:port addresses
	peerData1, err := peerMsg1.GetPeerAssignmentData()
	if err != nil {
		t.Fatalf("Failed to get peer data for client 1: %v", err)
	}

	// Peer ID should be client 2's address
	client2Addr := client2Conn.LocalAddr().(*net.UDPAddr)
	expectedPeerID2 := client2Addr.String()
	if peerData1.PeerID != expectedPeerID2 {
		t.Errorf("Expected peer ID '%s' for client 1, got: %s", expectedPeerID2, peerData1.PeerID)
	}

	peerData2, err := peerMsg2.GetPeerAssignmentData()
	if err != nil {
		t.Fatalf("Failed to get peer data for client 2: %v", err)
	}

	// Peer ID should be client 1's address  
	client1Addr := client1Conn.LocalAddr().(*net.UDPAddr)
	expectedPeerID1 := client1Addr.String()
	if peerData2.PeerID != expectedPeerID1 {
		t.Errorf("Expected peer ID '%s' for client 2, got: %s", expectedPeerID1, peerData2.PeerID)
	}

	// Check server state after pairing - clients should be removed from server memory
	if server.GetConnectedClients() != 0 {
		t.Errorf("Expected 0 connected clients after pairing, got: %d", server.GetConnectedClients())
	}

	if server.GetWaitingClients() != 0 {
		t.Errorf("Expected 0 waiting clients, got: %d", server.GetWaitingClients())
	}
}

func TestClientPing(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	// Connect client
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Register client
	registerMsg := NewClientRegisterMessage()
	data, _ := registerMsg.Serialize()
	clientConn.Write(data)

	// Consume waiting message
	buffer := make([]byte, 1024)
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	clientConn.Read(buffer)

	// Send ping
	pingMsg := NewClientPingMessage()
	pingData, _ := pingMsg.Serialize()
	clientConn.Write(pingData)

	// Give server time to process ping
	time.Sleep(100 * time.Millisecond)

	// Client should still be connected
	if server.GetConnectedClients() != 1 {
		t.Errorf("Expected client to stay connected after ping, got: %d clients", server.GetConnectedClients())
	}
}

func TestClientTimeout(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 500 * time.Millisecond, // Short timeout for testing
		PingInterval:  100 * time.Millisecond,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	// Connect client
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Register client
	registerMsg := NewClientRegisterMessage()
	data, _ := registerMsg.Serialize()
	clientConn.Write(data)

	// Consume waiting message
	buffer := make([]byte, 1024)
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	clientConn.Read(buffer)

	// Verify client is connected
	if server.GetConnectedClients() != 1 {
		t.Fatalf("Expected 1 connected client, got: %d", server.GetConnectedClients())
	}

	// Wait for timeout (don't send pings)
	time.Sleep(1 * time.Second)

	// Client should be disconnected due to timeout
	if server.GetConnectedClients() != 0 {
		t.Errorf("Expected client to be disconnected after timeout, got: %d clients", server.GetConnectedClients())
	}
}

func TestInvalidMessage(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	// Connect client
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Send invalid JSON
	invalidData := []byte("invalid json")
	clientConn.Write(invalidData)

	// Should receive error message
	buffer := make([]byte, 1024)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buffer)
	if err != nil {
		t.Fatalf("Failed to read error response: %v", err)
	}

	responseMsg, err := DeserializeMessage(buffer[:n])
	if err != nil {
		t.Fatalf("Failed to deserialize error response: %v", err)
	}

	if responseMsg.Type != ServerError {
		t.Errorf("Expected server error message, got: %v", responseMsg.Type)
	}
}

func TestDefaultServerConfig(t *testing.T) {
	config := DefaultServerConfig()
	
	if config.ListenAddress != ":3478" {
		t.Errorf("Expected ListenAddress to be ':3478', got %q", config.ListenAddress)
	}
	
	if config.ClientTimeout != 30*time.Second {
		t.Errorf("Expected ClientTimeout to be 30s, got %v", config.ClientTimeout)
	}
	
	if config.PingInterval != 10*time.Second {
		t.Errorf("Expected PingInterval to be 10s, got %v", config.PingInterval)
	}
	
	if config.MaxQueueSize != 100 {
		t.Errorf("Expected MaxQueueSize to be 100, got %d", config.MaxQueueSize)
	}
	
	if !config.EnableLogging {
		t.Error("Expected EnableLogging to be true")
	}
}

func TestMessageCreation(t *testing.T) {
	// Test server error message creation
	errorMsg := NewServerErrorMessage("test error", "TEST_ERR")
	if errorMsg.Type != ServerError {
		t.Errorf("Expected ServerError type, got %v", errorMsg.Type)
	}
	
	errorData, err := errorMsg.GetServerErrorData()
	if err != nil {
		t.Fatalf("Failed to get server error data: %v", err)
	}
	
	if errorData.ErrorMessage != "test error" {
		t.Errorf("Expected error message 'test error', got %q", errorData.ErrorMessage)
	}
	
	if errorData.ErrorCode != "TEST_ERR" {
		t.Errorf("Expected error code 'TEST_ERR', got %q", errorData.ErrorCode)
	}
	
	// Test peer ping message creation
	pingMsg := NewPeerPingMessage()
	if pingMsg.Type != PeerPing {
		t.Errorf("Expected PeerPing type, got %v", pingMsg.Type)
	}
	
	pingData, err := pingMsg.GetPeerPingData()
	if err != nil {
		t.Fatalf("Failed to get peer ping data: %v", err)
	}
	
	if pingData.Timestamp.IsZero() {
		t.Error("Expected ping timestamp to be set")
	}
	
	// Test peer pong message creation
	pongMsg := NewPeerPongMessage()
	if pongMsg.Type != PeerPong {
		t.Errorf("Expected PeerPong type, got %v", pongMsg.Type)
	}
	
	pongData, err := pongMsg.GetPeerPingData()
	if err != nil {
		t.Fatalf("Failed to get peer pong data: %v", err)
	}
	
	if pongData.Timestamp.IsZero() {
		t.Error("Expected pong timestamp to be set")
	}
}

func TestMessageSerialization(t *testing.T) {
	// Test serialization and deserialization of various message types
	messages := []*Message{
		NewClientRegisterMessage(),
		NewClientPingMessage(),
		NewWaitingForPeerMessage(),
		NewServerErrorMessage("test", "ERR"),
		NewPeerPingMessage(),
		NewPeerPongMessage(),
	}
	
	for _, originalMsg := range messages {
		// Serialize
		data, err := originalMsg.Serialize()
		if err != nil {
			t.Fatalf("Failed to serialize %v message: %v", originalMsg.Type, err)
		}
		
		// Deserialize
		deserializedMsg, err := DeserializeMessage(data)
		if err != nil {
			t.Fatalf("Failed to deserialize %v message: %v", originalMsg.Type, err)
		}
		
		if deserializedMsg.Type != originalMsg.Type {
			t.Errorf("Message type mismatch: expected %v, got %v", originalMsg.Type, deserializedMsg.Type)
		}
	}
}

func TestGetServerErrorDataInvalidType(t *testing.T) {
	// Test GetServerErrorData with wrong message type
	pingMsg := NewClientPingMessage()
	_, err := pingMsg.GetServerErrorData()
	if err == nil {
		t.Error("Expected error when calling GetServerErrorData on non-error message")
	}
	
	if err != ErrInvalidMessageType {
		t.Errorf("Expected ErrInvalidMessageType, got %v", err)
	}
}

func TestGetPeerPingDataInvalidType(t *testing.T) {
	// Test GetPeerPingData with wrong message type
	errorMsg := NewServerErrorMessage("test", "ERR")
	_, err := errorMsg.GetPeerPingData()
	if err == nil {
		t.Error("Expected error when calling GetPeerPingData on non-ping message")
	}
	
	if err != ErrInvalidMessageType {
		t.Errorf("Expected ErrInvalidMessageType, got %v", err)
	}
}
