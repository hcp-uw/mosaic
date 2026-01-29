package p2p

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/stun"
)

func TestClientConnect(t *testing.T) {
	// Start a test server
	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)

	// Create client
	clientConfig := DefaultClientConfig(serverAddr.String())
	client, err := NewClient(clientConfig)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test connect
	err = client.ConnectToStun()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.DisconnectFromStun()

	// Give time for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Should be in waiting state
	state := client.GetState()
	if state != StateWaiting {
		t.Errorf("Expected waiting state, got: %v", state)
	}
}

func TestClientPairingWithServer(t *testing.T) {
	// Start a test server
	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 10 * time.Second, // Increased timeout for stability
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	err := server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)

	// Create two clients
	client1Config := DefaultClientConfig(serverAddr.String())
	client1, err := NewClient(client1Config)
	if err != nil {
		t.Fatalf("Failed to create client 1: %v", err)
	}

	client2Config := DefaultClientConfig(serverAddr.String())
	client2, err := NewClient(client2Config)
	if err != nil {
		t.Fatalf("Failed to create client 2: %v", err)
	}

	// Use channels for thread-safe communication
	client1Paired := make(chan bool, 1)
	client2Paired := make(chan bool, 1)
	client1PeerChan := make(chan *PeerInfo, 1)
	client2PeerChan := make(chan *PeerInfo, 1)

	// Set up callbacks for client 1
	client1.OnStateChange(func(state ClientState) {
		if state == StatePaired {
			select {
			case client1Paired <- true:
			default:
			}
		}
	})

	client1.OnPeerAssigned(func(peerInfo *PeerInfo) {
		select {
		case client1PeerChan <- peerInfo:
		default:
		}
	})

	// Set up callbacks for client 2
	client2.OnStateChange(func(state ClientState) {
		if state == StatePaired {
			select {
			case client2Paired <- true:
			default:
			}
		}
	})

	client2.OnPeerAssigned(func(peerInfo *PeerInfo) {
		select {
		case client2PeerChan <- peerInfo:
		default:
		}
	})

	// Connect clients
	err = client1.ConnectToStun()
	if err != nil {
		t.Fatalf("Failed to connect client 1: %v", err)
	}

	err = client2.ConnectToStun()
	if err != nil {
		t.Fatalf("Failed to connect client 2: %v", err)
	}

	// Wait for both clients to be paired with timeout
	timeout := time.After(7 * time.Second)
	var client1PeerInfo, client2PeerInfo *PeerInfo

	// Wait for client 1 pairing
	select {
	case <-client1Paired:
	case <-timeout:
		client1.DisconnectFromStun()
		client2.DisconnectFromStun()
		t.Fatal("Timeout waiting for client 1 to be paired")
	}

	// Wait for client 2 pairing
	select {
	case <-client2Paired:
	case <-timeout:
		client1.DisconnectFromStun()
		client2.DisconnectFromStun()
		t.Fatal("Timeout waiting for client 2 to be paired")
	}

	// Wait for peer info to be received
	select {
	case client1PeerInfo = <-client1PeerChan:
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for client 1 peer info")
	}

	select {
	case client2PeerInfo = <-client2PeerChan:
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for client 2 peer info")
	}

	// Verify both clients are paired
	if client1.GetState() != StatePaired {
		t.Errorf("Expected client 1 to be paired, got: %v", client1.GetState())
	}

	if client2.GetState() != StatePaired {
		t.Errorf("Expected client 2 to be paired, got: %v", client2.GetState())
	}

	// Verify peer information
	if client1PeerInfo == nil {
		t.Error("Client 1 should have peer info")
	} else if !strings.HasPrefix(client1PeerInfo.ID, "127.0.0.1:") {
		t.Errorf("Expected client 1 peer ID to start with '127.0.0.1:', got: %s", client1PeerInfo.ID)
	}

	if client2PeerInfo == nil {
		t.Error("Client 2 should have peer info")
	} else if !strings.HasPrefix(client2PeerInfo.ID, "127.0.0.1:") {
		t.Errorf("Expected client 2 peer ID to start with '127.0.0.1:', got: %s", client2PeerInfo.ID)
	}

	// Clean disconnect after verification
	client1.DisconnectFromStun()
	client2.DisconnectFromStun()
}

func TestClientStateTransitions(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Initial state should be disconnected
	if client.GetState() != StateDisconnected {
		t.Errorf("Expected initial state to be disconnected, got: %v", client.GetState())
	}

	// Test state change callbacks
	var stateChanges []ClientState
	var mutex sync.Mutex

	client.OnStateChange(func(state ClientState) {
		mutex.Lock()
		stateChanges = append(stateChanges, state)
		mutex.Unlock()
	})

	// Connect should change state to connecting (then fail due to no server)
	err = client.ConnectToStun()
	// This will fail but should still change state

	time.Sleep(100 * time.Millisecond)

	mutex.Lock()
	if len(stateChanges) == 0 {
		t.Error("Expected state change callback to be called")
	}
	mutex.Unlock()

	client.DisconnectFromStun()
}

func TestClientErrorHandling(t *testing.T) {
	// Test with invalid server address
	client, err := NewClient(&ClientConfig{
		ServerAddress: "invalid-address",
	})

	if err == nil {
		t.Error("Expected error for invalid server address")
	}

	// Test with nil config
	_, err = NewClient(nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}

	// Test connecting when already connected
	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	err = server.Start(config)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)

	client, err = NewClient(DefaultClientConfig(serverAddr.String()))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.ConnectToStun()
	if err != nil {
		t.Fatalf("Failed to connect first time: %v", err)
	}
	defer client.DisconnectFromStun()

	// Try to connect again
	err = client.ConnectToStun()
	if err == nil {
		t.Error("Expected error when connecting already connected client")
	}
}

func TestClientPeerOperationsWithoutPeer(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test operations without peer connection
	err = client.ConnectToPeer()
	if err == nil {
		t.Error("Expected error when connecting to peer without assignment")
	}

	err = client.SendToPeer([]byte("test"))
	if err == nil {
		t.Error("Expected error when sending to peer without connection")
	}

	// Message receiving is now handled automatically via callbacks
	// No need to test ReceiveFromPeer as it no longer exists
}

func TestClientStateString(t *testing.T) {
	tests := []struct {
		state    ClientState
		expected string
	}{
		{StateDisconnected, "Disconnected"},
		{StateConnecting, "Connecting"},
		{StateWaiting, "Waiting"},
		{StatePaired, "Paired"},
		{StateConnectedToPeer, "ConnectedToPeer"},
		{ClientState(999), "Unknown"}, // Test unknown state
	}

	for _, test := range tests {
		result := test.state.String()
		if result != test.expected {
			t.Errorf("Expected %s.String() to return %q, got %q",
				test.state, test.expected, result)
		}
	}
}

func TestDefaultClientConfig(t *testing.T) {
	config := DefaultClientConfig("localhost:1234")

	if config.ServerAddress != "localhost:1234" {
		t.Errorf("Expected ServerAddress to be 'localhost:1234', got %q", config.ServerAddress)
	}

	if config.PingInterval != 10*time.Second {
		t.Errorf("Expected PingInterval to be 10s, got %v", config.PingInterval)
	}

	if config.ConnectTimeout != 30*time.Second {
		t.Errorf("Expected ConnectTimeout to be 30s, got %v", config.ConnectTimeout)
	}
}

func TestClientUtilityMethods(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test GetPeerInfo when no peer is assigned
	peerInfo := client.GetPeerInfo()
	if peerInfo != nil {
		t.Error("Expected GetPeerInfo to return nil when no peer assigned")
	}

	// Test IsPeerCommunicationAvailable when no peer is assigned
	available := client.IsPeerCommunicationAvailable()
	if available {
		t.Error("Expected IsPeerCommunicationAvailable to return false when no peer assigned")
	}

	// Manually set peer info to test the method
	testAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	client.mutex.Lock()
	client.peerInfo = &PeerInfo{
		Address: testAddr,
		ID:      "test-peer",
	}
	client.peerConn = &net.UDPConn{} // Mock connection
	client.state = StatePaired
	client.mutex.Unlock()

	// Test GetPeerInfo when peer is assigned
	peerInfo = client.GetPeerInfo()
	if peerInfo == nil {
		t.Error("Expected GetPeerInfo to return peer info when peer is assigned")
	} else {
		if peerInfo.ID != "test-peer" {
			t.Errorf("Expected peer ID to be 'test-peer', got %q", peerInfo.ID)
		}
		if peerInfo.Address.String() != "127.0.0.1:5678" {
			t.Errorf("Expected peer address to be '127.0.0.1:5678', got %q", peerInfo.Address.String())
		}
	}

	// Test IsPeerCommunicationAvailable when peer is assigned
	available = client.IsPeerCommunicationAvailable()
	if !available {
		t.Error("Expected IsPeerCommunicationAvailable to return true when peer is assigned and connected")
	}

	// Test IsPeerCommunicationAvailable when disconnected
	client.mutex.Lock()
	client.state = StateDisconnected
	client.mutex.Unlock()

	available = client.IsPeerCommunicationAvailable()
	if available {
		t.Error("Expected IsPeerCommunicationAvailable to return false when client is disconnected")
	}
}

func TestErrorCallbacks(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test error callback registration and notification
	var receivedError error
	var errorReceived sync.WaitGroup
	errorReceived.Add(1)

	client.OnError(func(err error) {
		receivedError = err
		errorReceived.Done()
	})

	// Trigger an error by calling notifyError
	testError := fmt.Errorf("test error")
	client.notifyError(testError)

	// Wait for error callback to be called
	done := make(chan bool)
	go func() {
		errorReceived.Wait()
		done <- true
	}()

	select {
	case <-done:
		if receivedError == nil {
			t.Error("Expected error callback to receive an error")
		} else if receivedError.Error() != "test error" {
			t.Errorf("Expected error message 'test error', got %q", receivedError.Error())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for error callback")
	}
}

func TestPeerCommunicationMethods(t *testing.T) {
	// Create a mock UDP connection for testing
	localAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		t.Fatalf("Failed to create UDP connection: %v", err)
	}
	defer conn.Close()

	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test sendPeerPing when no peer connection exists
	err = client.sendPeerPing()
	if err == nil {
		t.Error("Expected error when calling sendPeerPing without peer connection")
	}

	// Test sendPeerPong when no peer connection exists
	err = client.sendPeerPong()
	if err == nil {
		t.Error("Expected error when calling sendPeerPong without peer connection")
	}

	// Set up peer connection for successful tests
	peerAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	client.mutex.Lock()
	client.peerInfo = &PeerInfo{
		Address: peerAddr,
		ID:      "test-peer",
	}
	client.peerConn = conn
	client.mutex.Unlock()

	// Test sendPeerPing with valid peer connection
	err = client.sendPeerPing()
	if err != nil {
		t.Errorf("Expected sendPeerPing to succeed with valid peer connection, got error: %v", err)
	}

	// Test sendPeerPong with valid peer connection
	err = client.sendPeerPong()
	if err != nil {
		t.Errorf("Expected sendPeerPong to succeed with valid peer connection, got error: %v", err)
	}
}

func TestPeerMessageProcessing(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test STUN_PUNCH message filtering
	client.processPeerMessage([]byte("STUN_PUNCH"))
	// Should not trigger any callbacks - this is just a hole punching packet

	// Test peer ping message processing
	pingMsg := api.NewPeerPingMessage()
	pingData, _ := pingMsg.Serialize()

	// Set up peer connection so sendPeerPong works
	localAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		t.Fatalf("Failed to create UDP connection: %v", err)
	}
	defer conn.Close()

	peerAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	client.mutex.Lock()
	client.peerInfo = &PeerInfo{
		Address: peerAddr,
		ID:      "test-peer",
	}
	client.peerConn = conn
	client.mutex.Unlock()

	// Process peer ping - should trigger pong response
	client.processPeerMessage(pingData)

	// Test peer pong message processing
	pongMsg := api.NewPeerPongMessage()
	pongData, _ := pongMsg.Serialize()

	// Process peer pong - should update lastPeerPong time
	oldPongTime := client.lastPeerPong
	client.processPeerMessage(pongData)

	client.mutex.RLock()
	newPongTime := client.lastPeerPong
	client.mutex.RUnlock()

	if !newPongTime.After(oldPongTime) {
		t.Error("Expected lastPeerPong to be updated after receiving pong message")
	}

	// Test regular data message processing
	var receivedMessage []byte
	var messageReceived sync.WaitGroup
	messageReceived.Add(1)

	client.OnMessageReceived(func(data []byte) {
		receivedMessage = data
		messageReceived.Done()
	})

	testData := []byte("Hello, peer!")
	client.processPeerMessage(testData)

	// Wait for message callback
	done := make(chan bool)
	go func() {
		messageReceived.Wait()
		done <- true
	}()

	select {
	case <-done:
		if string(receivedMessage) != string(testData) {
			t.Errorf("Expected received message %q, got %q", testData, receivedMessage)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for message received callback")
	}
}

func TestEdgeCasesAndErrorScenarios(t *testing.T) {
	// Test NewClient with nil config
	_, err := NewClient(nil)
	if err == nil {
		t.Error("Expected error when creating client with nil config")
	}

	// Test with invalid server address
	_, err = NewClient(&ClientConfig{
		ServerAddress: "invalid:address:format",
	})
	if err == nil {
		t.Error("Expected error when creating client with invalid server address")
	}

	// Test ConnectToPeer without peer info
	client, _ := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})

	err = client.ConnectToPeer()
	if err == nil {
		t.Error("Expected error when calling ConnectToPeer without peer assignment")
	}

	// Test SendToPeer without peer connection
	err = client.SendToPeer([]byte("test"))
	if err == nil {
		t.Error("Expected error when calling SendToPeer without peer connection")
	}

	// Test multiple error callbacks
	var errorCount int
	var errorCountMutex sync.Mutex

	client.OnError(func(err error) {
		errorCountMutex.Lock()
		errorCount++
		errorCountMutex.Unlock()
	})

	client.OnError(func(err error) {
		errorCountMutex.Lock()
		errorCount++
		errorCountMutex.Unlock()
	})

	// Trigger error
	client.notifyError(fmt.Errorf("test error"))
	time.Sleep(100 * time.Millisecond) // Give callbacks time to execute

	errorCountMutex.Lock()
	finalCount := errorCount
	errorCountMutex.Unlock()

	if finalCount != 2 {
		t.Errorf("Expected 2 error callbacks to be called, got %d", finalCount)
	}

	// Test invalid message processing
	invalidJSON := []byte("{invalid json")
	client.processServerMessage(invalidJSON)
	// Should trigger error callback but not crash

	// Test unknown message type processing
	unknownMsg := &api.Message{
		Type: api.MessageType("unknown_type"),
	}
	client.processMessage(unknownMsg)
	// Should trigger error callback but not crash
}
