package networking

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/hcp-uw/mosaic/internal/networking/transport"
)

// TestBasicWorkflow tests the basic networking workflow end-to-end
func TestBasicWorkflow(t *testing.T) {
	// Generate key pairs for testing
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Test peer connection establishment
	nodeIP := NodeIP(net.ParseIP("127.0.0.1"))
	conn1, err := EstablishMockPeerConnection(nodeIP, privateKey1, mockDialer)
	if err != nil {
		t.Fatalf("Failed to establish mock peer connection: %v", err)
	}
	defer conn1.Close()

	conn2, err := EstablishMockPeerConnection(nodeIP, privateKey2, mockDialer)
	if err != nil {
		t.Fatalf("Failed to establish mock peer connection: %v", err)
	}
	defer conn2.Close()

	// Test CRDT creation and basic operations
	crdt1 := NewCRDT()
	_ = NewCRDT() // crdt2 for potential future use

	// Test peer verification functions
	if !PeerIsNew(publicKey1, crdt1) {
		t.Error("Expected peer to be new in empty CRDT")
	}

	// Test CRDT verification error (should fail since key is not in CRDT)
	if err := VerifyInCRDT(crdt1, publicKey1); err == nil {
		t.Error("Expected VerifyInCRDT to fail for key not in CRDT")
	}

	t.Logf("Basic workflow test completed successfully")
}

// TestCRDTIntegration tests CRDT functionality
func TestCRDTIntegration(t *testing.T) {
	// Generate key pair for testing
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create CRDT
	crdt := NewCRDT()

	// Verify initial state
	if len(crdt.JoinMessages) != 0 {
		t.Errorf("Expected empty CRDT, got %d join messages", len(crdt.JoinMessages))
	}

	// Test adding a join message (this will require accessing protocol types)
	_ = JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	// This would normally be done through the protocol layer
	t.Logf("CRDT integration test completed - would need protocol integration for full test")
}

// TestMockConnections tests that our mock connections work correctly
func TestMockConnections(t *testing.T) {
	// Generate key pair
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Setup mock dialer
	mockDialer := transport.NewMockUDPDialer()

	// Test STUN connection
	nodeIP := NodeIP(net.ParseIP("127.0.0.1"))
	stunConn, err := EstablishMockStunConnection(nodeIP, mockDialer)
	if err != nil {
		t.Fatalf("Failed to establish mock STUN connection: %v", err)
	}
	defer stunConn.Close()

	// Test peer connection
	peerConn, err := EstablishMockPeerConnection(nodeIP, privateKey, mockDialer)
	if err != nil {
		t.Fatalf("Failed to establish mock peer connection: %v", err)
	}
	defer peerConn.Close()

	// Verify connections have correct remote addresses
	expectedAddr := "127.0.0.1:8080"
	if stunConn.RemoteAddr().String() != expectedAddr {
		t.Errorf("Expected STUN address %s, got %s", expectedAddr, stunConn.RemoteAddr().String())
	}

	expectedPeerAddr := "127.0.0.1:9090"
	if peerConn.RemoteAddr().String() != expectedPeerAddr {
		t.Errorf("Expected peer address %s, got %s", expectedPeerAddr, peerConn.RemoteAddr().String())
	}

	t.Logf("Mock connections test completed successfully")
}