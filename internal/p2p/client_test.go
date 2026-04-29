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
	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	if err := server.Start(config); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)

	client, err := NewClient(DefaultClientConfig(serverAddr.String()))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.ConnectToStun(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.DisconnectFromStun()

	time.Sleep(100 * time.Millisecond)

	if state := client.GetState(); state != StateLeader {
		t.Errorf("Expected leader state for first client, got: %v", state)
	}
}

func TestClientPairingWithServer(t *testing.T) {
	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 10 * time.Second,
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	if err := server.Start(config); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)

	client1, err := NewClient(DefaultClientConfig(serverAddr.String()))
	if err != nil {
		t.Fatalf("Failed to create client 1: %v", err)
	}
	client2, err := NewClient(DefaultClientConfig(serverAddr.String()))
	if err != nil {
		t.Fatalf("Failed to create client 2: %v", err)
	}
	defer client1.DisconnectFromStun()
	defer client2.DisconnectFromStun()

	client1Peers := make(chan *PeerInfo, 1)
	client2Peers := make(chan *PeerInfo, 1)

	client1.OnPeerAssigned(func(peerInfo *PeerInfo) {
		select {
		case client1Peers <- peerInfo:
		default:
		}
	})
	client2.OnPeerAssigned(func(peerInfo *PeerInfo) {
		select {
		case client2Peers <- peerInfo:
		default:
		}
	})

	if err := client1.ConnectToStun(); err != nil {
		t.Fatalf("Failed to connect client 1: %v", err)
	}
	// Give client1 a beat to be registered as leader before client2
	// joins; without this, the server can race-assign leader to client2.
	time.Sleep(100 * time.Millisecond)
	if err := client2.ConnectToStun(); err != nil {
		t.Fatalf("Failed to connect client 2: %v", err)
	}

	var client1PeerInfo, client2PeerInfo *PeerInfo

	select {
	case client1PeerInfo = <-client1Peers:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for client 1 peer assignment")
	}

	select {
	case client2PeerInfo = <-client2Peers:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for client 2 peer assignment")
	}

	if client1.GetState() != StateLeader {
		t.Errorf("Expected client 1 to remain leader, got: %v", client1.GetState())
	}
	if client2.GetState() != StatePaired {
		t.Errorf("Expected client 2 to be paired, got: %v", client2.GetState())
	}

	if client1PeerInfo == nil || !strings.HasPrefix(client1PeerInfo.ID, "127.0.0.1:") {
		t.Fatalf("Expected valid peer info for client 1, got: %#v", client1PeerInfo)
	}
	if client2PeerInfo == nil || !strings.HasPrefix(client2PeerInfo.ID, "127.0.0.1:") {
		t.Fatalf("Expected valid peer info for client 2, got: %#v", client2PeerInfo)
	}
}

func TestClientStateTransitions(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "127.0.0.1:65535",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client.GetState() != StateDisconnected {
		t.Errorf("Expected initial state to be disconnected, got: %v", client.GetState())
	}

	var (
		stateChanges []ClientState
		mutex        sync.Mutex
	)

	client.OnStateChange(func(state ClientState) {
		mutex.Lock()
		stateChanges = append(stateChanges, state)
		mutex.Unlock()
	})

	if err := client.ConnectToStun(); err != nil {
		t.Fatalf("ConnectToStun should succeed with an unreachable server: %v", err)
	}
	defer client.DisconnectFromStun()

	time.Sleep(100 * time.Millisecond)

	mutex.Lock()
	defer mutex.Unlock()
	if len(stateChanges) == 0 {
		t.Error("Expected at least one state change callback")
	}
	if stateChanges[0] != StateConnecting {
		t.Errorf("Expected first state change to be connecting, got: %v", stateChanges[0])
	}
}

func TestClientErrorHandling(t *testing.T) {
	if _, err := NewClient(&ClientConfig{ServerAddress: "invalid-address"}); err == nil {
		t.Error("Expected error for invalid server address")
	}

	if _, err := NewClient(nil); err == nil {
		t.Error("Expected error for nil config")
	}

	config := &stun.ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		EnableLogging: false,
	}

	server := stun.NewServer(config)
	if err := server.Start(config); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.GetConn().LocalAddr().(*net.UDPAddr)
	client, err := NewClient(DefaultClientConfig(serverAddr.String()))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.DisconnectFromStun()

	if err := client.ConnectToStun(); err != nil {
		t.Fatalf("Failed to connect first time: %v", err)
	}
	if err := client.ConnectToStun(); err == nil {
		t.Error("Expected error when connecting an already connected client")
	}
}

func TestClientPeerOperationsWithoutPeer(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.ConnectToPeer(nil); err == nil {
		t.Error("Expected error when connecting to a nil peer")
	}

	msg := api.NewPeerTextMessage("test", "sender")
	if err := client.SendToPeer("missing-peer", msg); err == nil {
		t.Error("Expected error when sending to a missing peer")
	}
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
		{StateLeader, "Leader"},
		{ClientState(999), "Unknown"},
	}

	for _, test := range tests {
		if result := test.state.String(); result != test.expected {
			t.Errorf("Expected %v.String() to return %q, got %q", test.state, test.expected, result)
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

	if peerInfo := client.GetPeerById("missing"); peerInfo != nil {
		t.Error("Expected nil when peer is missing")
	}
	if available := client.IsPeerCommunicationAvailable(); available {
		t.Error("Expected peer communication to be unavailable without peers")
	}

	testAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatalf("Failed to create UDP connection: %v", err)
	}
	defer conn.Close()

	client.mutex.Lock()
	client.peers["test-peer"] = &PeerInfo{
		Address: testAddr,
		Conn:    conn,
		ID:      "test-peer",
	}
	client.state = StatePaired
	client.mutex.Unlock()

	peerInfo := client.GetPeerById("test-peer")
	if peerInfo == nil {
		t.Fatal("Expected peer info to be present")
	}
	if peerInfo.ID != "test-peer" {
		t.Errorf("Expected peer ID to be 'test-peer', got %q", peerInfo.ID)
	}
	if peerInfo.Address.String() != "127.0.0.1:5678" {
		t.Errorf("Expected peer address to be '127.0.0.1:5678', got %q", peerInfo.Address.String())
	}
	if available := client.IsPeerCommunicationAvailable(); !available {
		t.Error("Expected peer communication to be available when a connected peer exists")
	}

	client.mutex.Lock()
	client.state = StateDisconnected
	client.mutex.Unlock()
	if available := client.IsPeerCommunicationAvailable(); available {
		t.Error("Expected peer communication to be unavailable when disconnected")
	}
}

func TestErrorCallbacks(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	var receivedError error
	var errorReceived sync.WaitGroup
	errorReceived.Add(1)

	client.OnError(func(err error) {
		receivedError = err
		errorReceived.Done()
	})

	client.notifyError(fmt.Errorf("test error"))

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

	if err := client.sendPeerPing("missing-peer"); err == nil {
		t.Error("Expected error when calling sendPeerPing without a peer")
	}
	if err := client.sendPeerPong("missing-peer"); err == nil {
		t.Error("Expected error when calling sendPeerPong without a peer")
	}

	peerAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	client.mutex.Lock()
	client.peers["test-peer"] = &PeerInfo{
		Address: peerAddr,
		Conn:    conn,
		ID:      "test-peer",
	}
	client.mutex.Unlock()

	if err := client.sendPeerPing("test-peer"); err != nil {
		t.Errorf("Expected sendPeerPing to succeed, got error: %v", err)
	}
	if err := client.sendPeerPong("test-peer"); err != nil {
		t.Errorf("Expected sendPeerPong to succeed, got error: %v", err)
	}
}

func TestPeerMessageProcessing(t *testing.T) {
	client, err := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	client.processPeerMessage([]byte("STUN_PUNCH"))

	localAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		t.Fatalf("Failed to create UDP connection: %v", err)
	}
	defer conn.Close()

	peerAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5678")
	client.mutex.Lock()
	client.peers["test-peer"] = &PeerInfo{
		Address:      peerAddr,
		Conn:         conn,
		ID:           "test-peer",
		LastPeerPong: time.Now().Add(-time.Hour),
	}
	client.mutex.Unlock()

	pingMsg := api.NewPeerPingMessage(api.NewSignature("test-peer"))
	pingData, _ := pingMsg.Serialize()
	client.processPeerMessage(pingData)

	pongMsg := api.NewPeerPongMessage(api.NewSignature("test-peer"))
	pongData, _ := pongMsg.Serialize()
	oldPongTime := client.GetPeerById("test-peer").LastPeerPong
	client.processPeerMessage(pongData)
	newPongTime := client.GetPeerById("test-peer").LastPeerPong
	if !newPongTime.After(oldPongTime) {
		t.Error("Expected LastPeerPong to update after receiving pong")
	}

	var receivedMessage []byte
	var messageReceived sync.WaitGroup
	messageReceived.Add(1)
	client.OnMessageReceived(func(data []byte) {
		receivedMessage = data
		messageReceived.Done()
	})

	textMsg := api.NewPeerTextMessage("Hello, peer!", "test-peer")
	textData, _ := textMsg.Serialize()
	client.processPeerMessage(textData)

	done := make(chan bool)
	go func() {
		messageReceived.Wait()
		done <- true
	}()

	select {
	case <-done:
		if string(receivedMessage) != "Hello, peer!" {
			t.Errorf("Expected received message %q, got %q", "Hello, peer!", receivedMessage)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for message received callback")
	}
}

func TestEdgeCasesAndErrorScenarios(t *testing.T) {
	if _, err := NewClient(nil); err == nil {
		t.Error("Expected error when creating client with nil config")
	}
	if _, err := NewClient(&ClientConfig{ServerAddress: "invalid:address:format"}); err == nil {
		t.Error("Expected error when creating client with invalid server address")
	}

	client, _ := NewClient(&ClientConfig{
		ServerAddress: "localhost:1234",
	})

	if err := client.ConnectToPeer(nil); err == nil {
		t.Error("Expected error when calling ConnectToPeer without peer assignment")
	}

	msg := api.NewPeerTextMessage("test", "sender")
	if err := client.SendToPeer("missing-peer", msg); err == nil {
		t.Error("Expected error when calling SendToPeer without peer connection")
	}

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

	client.notifyError(fmt.Errorf("test error"))
	time.Sleep(100 * time.Millisecond)

	errorCountMutex.Lock()
	finalCount := errorCount
	errorCountMutex.Unlock()
	if finalCount != 2 {
		t.Errorf("Expected 2 error callbacks to be called, got %d", finalCount)
	}

	client.processServerMessage([]byte("{invalid json"))
	client.processMessage(&api.Message{Type: api.MessageType("unknown_type")})
}
