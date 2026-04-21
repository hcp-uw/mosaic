package stun

import (
	"net"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 5 * time.Second,
		PingInterval:  1 * time.Second,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	if err := server.Start(config); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	return server
}

func readUDPMessage(t *testing.T, conn *net.UDPConn) *api.Message {
	t.Helper()

	buffer := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatalf("Failed to read UDP message: %v", err)
	}

	msg, err := api.DeserializeMessage(buffer[:n])
	if err != nil {
		t.Fatalf("Failed to deserialize UDP message: %v", err)
	}

	return msg
}

func TestServerStartStop(t *testing.T) {
	server := newTestServer(t)

	time.Sleep(100 * time.Millisecond)

	if err := server.Stop(); err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}
}

func TestClientRegistrationAssignsLeader(t *testing.T) {
	server := newTestServer(t)
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer clientConn.Close()

	registerMsg := api.NewClientRegisterMessage("")
	data, err := registerMsg.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize message: %v", err)
	}

	if _, err := clientConn.Write(data); err != nil {
		t.Fatalf("Failed to send registration: %v", err)
	}

	first := readUDPMessage(t, clientConn)
	second := readUDPMessage(t, clientConn)

	if first.Type != api.RegisterSuccess {
		t.Errorf("Expected first message to be register success, got: %v", first.Type)
	}
	if second.Type != api.AssignedAsLeader {
		t.Errorf("Expected second message to be leader assignment, got: %v", second.Type)
	}

	if server.GetConnectedClients() != 1 {
		t.Errorf("Expected 1 connected client, got: %d", server.GetConnectedClients())
	}
	if server.GetWaitingClients() != 0 {
		t.Errorf("Expected 0 waiting clients, got: %d", server.GetWaitingClients())
	}
}

func TestClientPairing(t *testing.T) {
	server := newTestServer(t)
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)

	client1Conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect client 1: %v", err)
	}
	defer client1Conn.Close()

	client2Conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect client 2: %v", err)
	}
	defer client2Conn.Close()

	registerMsg1 := api.NewClientRegisterMessage("")
	data1, _ := registerMsg1.Serialize()
	if _, err := client1Conn.Write(data1); err != nil {
		t.Fatalf("Failed to send registration for client 1: %v", err)
	}

	if msg := readUDPMessage(t, client1Conn); msg.Type != api.RegisterSuccess {
		t.Fatalf("Expected register success for client 1, got: %v", msg.Type)
	}
	if msg := readUDPMessage(t, client1Conn); msg.Type != api.AssignedAsLeader {
		t.Fatalf("Expected leader assignment for client 1, got: %v", msg.Type)
	}

	registerMsg2 := api.NewClientRegisterMessage("")
	data2, _ := registerMsg2.Serialize()
	if _, err := client2Conn.Write(data2); err != nil {
		t.Fatalf("Failed to send registration for client 2: %v", err)
	}

	client2Register := readUDPMessage(t, client2Conn)
	client2Peer := readUDPMessage(t, client2Conn)
	client1Peer := readUDPMessage(t, client1Conn)

	if client2Register.Type != api.RegisterSuccess {
		t.Errorf("Expected register success for client 2, got: %v", client2Register.Type)
	}
	if client2Peer.Type != api.PeerAssignment {
		t.Errorf("Expected peer assignment for client 2, got: %v", client2Peer.Type)
	}
	if client1Peer.Type != api.PeerAssignment {
		t.Errorf("Expected peer assignment for client 1, got: %v", client1Peer.Type)
	}

	peerData1, err := client1Peer.GetPeerAssignmentData()
	if err != nil {
		t.Fatalf("Failed to get peer data for client 1: %v", err)
	}
	peerData2, err := client2Peer.GetPeerAssignmentData()
	if err != nil {
		t.Fatalf("Failed to get peer data for client 2: %v", err)
	}

	client1Addr := client1Conn.LocalAddr().(*net.UDPAddr).String()
	client2Addr := client2Conn.LocalAddr().(*net.UDPAddr).String()

	if peerData1.PeerID != client2Addr {
		t.Errorf("Expected client 1 peer ID %q, got %q", client2Addr, peerData1.PeerID)
	}
	if peerData2.PeerID != client1Addr {
		t.Errorf("Expected client 2 peer ID %q, got %q", client1Addr, peerData2.PeerID)
	}

	if server.GetConnectedClients() != 1 {
		t.Errorf("Expected only the leader to remain tracked, got: %d clients", server.GetConnectedClients())
	}
	if server.GetWaitingClients() != 0 {
		t.Errorf("Expected 0 waiting clients, got: %d", server.GetWaitingClients())
	}
}

func TestClientPing(t *testing.T) {
	server := newTestServer(t)
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	registerMsg := api.NewClientRegisterMessage("")
	data, _ := registerMsg.Serialize()
	clientConn.Write(data)

	registerResp := readUDPMessage(t, clientConn)
	if registerResp.Type != api.RegisterSuccess {
		t.Fatalf("Expected register success, got: %v", registerResp.Type)
	}
	_ = readUDPMessage(t, clientConn)

	pingMsg := api.NewClientPingMessage(api.NewSignature(clientConn.LocalAddr().String()))
	pingData, _ := pingMsg.Serialize()
	clientConn.Write(pingData)

	time.Sleep(100 * time.Millisecond)

	if server.GetConnectedClients() != 1 {
		t.Errorf("Expected client to stay connected after ping, got: %d clients", server.GetConnectedClients())
	}
}

func TestClientTimeout(t *testing.T) {
	config := &ServerConfig{
		ListenAddress: "127.0.0.1:0",
		ClientTimeout: 500 * time.Millisecond,
		PingInterval:  100 * time.Millisecond,
		MaxQueueSize:  10,
		EnableLogging: false,
	}

	server := NewServer(config)
	if err := server.Start(config); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	registerMsg := api.NewClientRegisterMessage("")
	data, _ := registerMsg.Serialize()
	clientConn.Write(data)

	if msg := readUDPMessage(t, clientConn); msg.Type != api.RegisterSuccess {
		t.Fatalf("Expected register success, got: %v", msg.Type)
	}
	_ = readUDPMessage(t, clientConn)

	if server.GetConnectedClients() != 1 {
		t.Fatalf("Expected 1 connected client, got: %d", server.GetConnectedClients())
	}

	time.Sleep(1 * time.Second)

	if server.GetConnectedClients() != 0 {
		t.Errorf("Expected client to be disconnected after timeout, got: %d clients", server.GetConnectedClients())
	}
}

func TestInvalidMessage(t *testing.T) {
	server := newTestServer(t)
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().(*net.UDPAddr)
	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("invalid json")); err != nil {
		t.Fatalf("Failed to write invalid payload: %v", err)
	}

	responseMsg := readUDPMessage(t, clientConn)
	if responseMsg.Type != api.ServerError {
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
	sign := api.NewSignature("test-sender")

	errorMsg := api.NewServerErrorMessage("test error", "TEST_ERR")
	if errorMsg.Type != api.ServerError {
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

	pingMsg := api.NewPeerPingMessage(sign)
	if pingMsg.Type != api.PeerPing {
		t.Errorf("Expected PeerPing type, got %v", pingMsg.Type)
	}
	pingData, err := pingMsg.GetPeerPingData()
	if err != nil {
		t.Fatalf("Failed to get peer ping data: %v", err)
	}
	if pingData.Timestamp.IsZero() {
		t.Error("Expected ping timestamp to be set")
	}

	pongMsg := api.NewPeerPongMessage(sign)
	if pongMsg.Type != api.PeerPong {
		t.Errorf("Expected PeerPong type, got %v", pongMsg.Type)
	}
	pongData, err := pongMsg.GetPeerPongData()
	if err != nil {
		t.Fatalf("Failed to get peer pong data: %v", err)
	}
	if pongData.Timestamp.IsZero() {
		t.Error("Expected pong timestamp to be set")
	}
}

func TestMessageSerialization(t *testing.T) {
	sign := api.NewSignature("test-sender")
	messages := []*api.Message{
		api.NewClientRegisterMessage(""),
		api.NewClientPingMessage(sign),
		api.NewWaitingForPeerMessage(),
		api.NewServerErrorMessage("test", "ERR"),
		api.NewPeerPingMessage(sign),
		api.NewPeerPongMessage(sign),
	}

	for _, originalMsg := range messages {
		data, err := originalMsg.Serialize()
		if err != nil {
			t.Fatalf("Failed to serialize %v message: %v", originalMsg.Type, err)
		}

		deserializedMsg, err := api.DeserializeMessage(data)
		if err != nil {
			t.Fatalf("Failed to deserialize %v message: %v", originalMsg.Type, err)
		}

		if deserializedMsg.Type != originalMsg.Type {
			t.Errorf("Message type mismatch: expected %v, got %v", originalMsg.Type, deserializedMsg.Type)
		}
	}
}

func TestGetServerErrorDataInvalidType(t *testing.T) {
	pingMsg := api.NewClientPingMessage(api.NewSignature("test-sender"))
	_, err := pingMsg.GetServerErrorData()
	if err == nil {
		t.Error("Expected error when calling GetServerErrorData on non-error message")
	}
	if err != api.ErrInvalidMessageType {
		t.Errorf("Expected ErrInvalidMessageType, got %v", err)
	}
}

func TestGetPeerPingDataInvalidType(t *testing.T) {
	errorMsg := api.NewServerErrorMessage("test", "ERR")
	_, err := errorMsg.GetPeerPingData()
	if err == nil {
		t.Error("Expected error when calling GetPeerPingData on non-ping message")
	}
	if err != api.ErrInvalidMessageType {
		t.Errorf("Expected ErrInvalidMessageType, got %v", err)
	}
}
