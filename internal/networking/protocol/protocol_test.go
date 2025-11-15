package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func TestMessageRouter_SetDataFlow(t *testing.T) {
	// Generate a key pair for testing
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create message router
	router := NewMessageRouter(privateKey)

	// Create a test store request
	storeRequest := StoreRequest{
		DataHash:  []byte("test-hash"),
		Size:      1024,
		Timestamp: time.Now(),
	}

	// Sign the store request
	signedStoreRequest, err := NewSignedStoreRequest(storeRequest, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign store request: %v", err)
	}

	// Create peer message wrapper
	peerMessage := PeerMessage[*SignedStoreRequest]{
		MessageType: PeerDataMessage,
		Data:        signedStoreRequest,
		Timestamp:   time.Now().Unix(),
	}

	// Serialize the message
	rawMessage, err := json.Marshal(peerMessage)
	if err != nil {
		t.Fatalf("Failed to marshal peer message: %v", err)
	}

	// Route the message through the router
	response, err := router.RouteMessage(rawMessage)
	if err != nil {
		t.Fatalf("Failed to route message: %v", err)
	}

	// Parse the response
	var signedAck *SignedStoreAcknowledge
	if err := json.Unmarshal(response, &signedAck); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Verify the response
	if err := signedAck.Verify(); err != nil {
		t.Fatalf("Response signature verification failed: %v", err)
	}

	// Check that it's signed by the expected key
	if !signedAck.PublicKey.Equal(publicKey) {
		t.Errorf("Response signed by wrong key")
	}

	// Check response data
	if signedAck.Data.Status != StoreAccepted {
		t.Errorf("Expected StoreAccepted, got %v", signedAck.Data.Status)
	}

	// Check that the request hash matches
	if string(signedAck.Data.RequestHash) != string(storeRequest.DataHash) {
		t.Errorf("Request hash mismatch")
	}

	t.Logf("Successfully processed SetData request and received acknowledgment")
}

func TestMessageRouter_UnknownMessageType(t *testing.T) {
	// Generate a key pair for testing
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create message router
	router := NewMessageRouter(privateKey)

	// Create message with unknown type
	unknownMessage := struct {
		MessageType PeerMessageCode `json:"message_type"`
		Data        string          `json:"data"`
	}{
		MessageType: PeerMessageCode(999), // Unknown type
		Data:        "test",
	}

	rawMessage, err := json.Marshal(unknownMessage)
	if err != nil {
		t.Fatalf("Failed to marshal message: %v", err)
	}

	// Route the message - should fail
	_, err = router.RouteMessage(rawMessage)
	if err == nil {
		t.Errorf("Expected error for unknown message type, but got none")
	}

	t.Logf("Correctly rejected unknown message type: %v", err)
}

func TestNewSignedJoinMessage(t *testing.T) {
	// Generate key pair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create join message
	joinMsg := JoinMessage{
		NodeID:    "test_node",
		PublicKey: publicKey,
	}

	// Sign the message
	signedJoinMsg, err := NewSignedJoinMessage(joinMsg, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign join message: %v", err)
	}

	// Verify the signature
	if err := signedJoinMsg.Verify(); err != nil {
		t.Errorf("Signature verification failed: %v", err)
	}

	// Verify the data
	if signedJoinMsg.Data.NodeID != joinMsg.NodeID {
		t.Errorf("Expected NodeID %s, got %s", joinMsg.NodeID, signedJoinMsg.Data.NodeID)
	}

	if !signedJoinMsg.Data.PublicKey.Equal(joinMsg.PublicKey) {
		t.Errorf("Public key mismatch")
	}
}

func TestNewSignedStoreRequest(t *testing.T) {
	// Generate key pair
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create store request
	storeReq := StoreRequest{
		DataHash:  []byte("test-hash"),
		Size:      1024,
		Timestamp: time.Now(),
	}

	// Sign the request
	signedStoreReq, err := NewSignedStoreRequest(storeReq, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign store request: %v", err)
	}

	// Verify the signature
	if err := signedStoreReq.Verify(); err != nil {
		t.Errorf("Signature verification failed: %v", err)
	}

	// Verify the data
	if string(signedStoreReq.Data.DataHash) != string(storeReq.DataHash) {
		t.Errorf("Data hash mismatch")
	}

	if signedStoreReq.Data.Size != storeReq.Size {
		t.Errorf("Expected size %d, got %d", storeReq.Size, signedStoreReq.Data.Size)
	}
}

func TestNewSignedStoreAcknowledge(t *testing.T) {
	// Generate key pair
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create store acknowledge
	storeAck := StoreAcknowledge{
		RequestHash: []byte("test-hash"),
		Status:      StoreAccepted,
		Timestamp:   time.Now(),
	}

	// Sign the acknowledge
	signedStoreAck, err := NewSignedStoreAcknowledge(storeAck, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign store acknowledge: %v", err)
	}

	// Verify the signature
	if err := signedStoreAck.Verify(); err != nil {
		t.Errorf("Signature verification failed: %v", err)
	}

	// Verify the data
	if string(signedStoreAck.Data.RequestHash) != string(storeAck.RequestHash) {
		t.Errorf("Request hash mismatch")
	}

	if signedStoreAck.Data.Status != storeAck.Status {
		t.Errorf("Expected status %v, got %v", storeAck.Status, signedStoreAck.Data.Status)
	}
}