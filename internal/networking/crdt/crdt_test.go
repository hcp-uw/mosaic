package crdt

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/protocol"
)

func TestNew(t *testing.T) {
	crdt := New()

	if crdt.JoinMessages == nil {
		t.Error("Expected JoinMessages map to be initialized")
	}

	if crdt.FileManifest == nil {
		t.Error("Expected FileManifest map to be initialized")
	}

	if len(crdt.JoinMessages) != 0 {
		t.Errorf("Expected empty JoinMessages, got %d entries", len(crdt.JoinMessages))
	}

	if len(crdt.FileManifest) != 0 {
		t.Errorf("Expected empty FileManifest, got %d entries", len(crdt.FileManifest))
	}
}

func TestAddJoinMessage(t *testing.T) {
	// Generate key pair for testing
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	crdt := New()

	// Create test join message
	joinMsg := protocol.JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	signedJoinMsg, err := protocol.NewSignedJoinMessage(joinMsg, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign join message: %v", err)
	}

	// Test adding valid join message
	events, err := crdt.AddJoinMessage("test_node", signedJoinMsg)
	if err != nil {
		t.Fatalf("Failed to add join message: %v", err)
	}

	// Should get a NewPeerDetected event
	if len(events) != 1 || events[0].Type != NewPeerDetected {
		t.Errorf("Expected NewPeerDetected event, got %d events", len(events))
	}

	// Verify message was added
	if len(crdt.JoinMessages) != 1 {
		t.Errorf("Expected 1 join message, got %d", len(crdt.JoinMessages))
	}

	if _, exists := crdt.JoinMessages["test_node"]; !exists {
		t.Error("Expected 'test_node' join message to exist")
	}
}

func TestAddJoinMessage_ConflictResolution(t *testing.T) {
	// Generate key pair for testing
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	crdt := New()

	// Create first join message (older)
	joinMsg1 := protocol.JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	signedJoinMsg1, err := protocol.NewSignedJoinMessage(joinMsg1, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign first join message: %v", err)
	}

	// Add first message
	_, err = crdt.AddJoinMessage("test_node", signedJoinMsg1)
	if err != nil {
		t.Fatalf("Failed to add first join message: %v", err)
	}

	// Wait a bit to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Create second join message (newer)
	joinMsg2 := protocol.JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	signedJoinMsg2, err := protocol.NewSignedJoinMessage(joinMsg2, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign second join message: %v", err)
	}

	// Add second message (should replace first due to newer timestamp)
	events, err := crdt.AddJoinMessage("test_node", signedJoinMsg2)
	if err != nil {
		t.Fatalf("Failed to add second join message: %v", err)
	}

	// Should get a ConflictResolved event
	if len(events) != 1 || events[0].Type != ConflictResolved {
		t.Errorf("Expected ConflictResolved event, got %d events", len(events))
	}

	// Verify only one message exists and it's the newer one
	if len(crdt.JoinMessages) != 1 {
		t.Errorf("Expected 1 join message, got %d", len(crdt.JoinMessages))
	}

	storedMsg := crdt.JoinMessages["test_node"]
	if !storedMsg.Timestamp.Equal(signedJoinMsg2.Timestamp) {
		t.Error("Expected newer message to be stored (based on timestamp)")
	}
}

func TestMerge(t *testing.T) {
	// Generate key pairs for testing
	publicKey1, privateKey1, _ := ed25519.GenerateKey(rand.Reader)
	publicKey2, privateKey2, _ := ed25519.GenerateKey(rand.Reader)

	// Create first CRDT with one join message
	crdt1 := New()
	joinMsg1 := protocol.JoinMessage{
		NodeID:    "node1",
		PublicKey: publicKey1,
	}
	signedJoinMsg1, _ := protocol.NewSignedJoinMessage(joinMsg1, privateKey1)
	_, _ = crdt1.AddJoinMessage("node1", signedJoinMsg1)

	// Create second CRDT with different join message
	crdt2 := New()
	joinMsg2 := protocol.JoinMessage{
		NodeID:    "node2",
		PublicKey: publicKey2,
	}
	signedJoinMsg2, _ := protocol.NewSignedJoinMessage(joinMsg2, privateKey2)
	_, _ = crdt2.AddJoinMessage("node2", signedJoinMsg2)

	// Merge crdt2 into crdt1
	_, err := crdt1.Merge(crdt2)
	if err != nil {
		t.Fatalf("Failed to merge CRDTs: %v", err)
	}

	// Verify both join messages exist in crdt1
	if len(crdt1.JoinMessages) != 2 {
		t.Errorf("Expected 2 join messages after merge, got %d", len(crdt1.JoinMessages))
	}

	if _, exists := crdt1.JoinMessages["node1"]; !exists {
		t.Error("Expected node1 join message to exist after merge")
	}

	if _, exists := crdt1.JoinMessages["node2"]; !exists {
		t.Error("Expected node2 join message to exist after merge")
	}
}

func TestHasPublicKey(t *testing.T) {
	// Generate key pairs for testing
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, unknownPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate unknown key pair: %v", err)
	}
	unknownPublicKey := unknownPrivateKey.Public().(ed25519.PublicKey)

	// Create CRDT with one join message
	crdt := New()
	joinMsg := protocol.JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey1,
	}
	signedJoinMsg, err := protocol.NewSignedJoinMessage(joinMsg, privateKey1)
	if err != nil {
		t.Fatalf("Failed to sign join message: %v", err)
	}
	_, _ = crdt.AddJoinMessage("test_node", signedJoinMsg)

	// Test existing public key
	if !crdt.HasPublicKey(publicKey1) {
		t.Error("Expected HasPublicKey to return true for existing key")
	}

	// Test non-existing public key
	if crdt.HasPublicKey(unknownPublicKey) {
		t.Error("Expected HasPublicKey to return false for unknown key")
	}
}

func TestEquals(t *testing.T) {
	// Generate key pair for testing
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create two identical CRDTs
	crdt1 := New()
	crdt2 := New()

	// They should be equal when empty
	if !crdt1.Equals(crdt2) {
		t.Error("Expected empty CRDTs to be equal")
	}

	// Add same join message to both
	joinMsg := protocol.JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	signedJoinMsg, err := protocol.NewSignedJoinMessage(joinMsg, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign join message: %v", err)
	}

	_, _ = crdt1.AddJoinMessage("test_node", signedJoinMsg)
	_, _ = crdt2.AddJoinMessage("test_node", signedJoinMsg)

	// They should still be equal
	if !crdt1.Equals(crdt2) {
		t.Error("Expected CRDTs with same data to be equal")
	}

	// Add different message to crdt2
	joinMsg2 := protocol.JoinMessage{
		NodeID:    "test_node2",
		PublicKey: publicKey,
	}

	signedJoinMsg2, _ := protocol.NewSignedJoinMessage(joinMsg2, privateKey)
	_, _ = crdt2.AddJoinMessage("test_node2", signedJoinMsg2)

	// They should no longer be equal
	if crdt1.Equals(crdt2) {
		t.Error("Expected CRDTs with different data to not be equal")
	}

	// Test nil comparison
	if crdt1.Equals(nil) {
		t.Error("Expected CRDT to not equal nil")
	}
}