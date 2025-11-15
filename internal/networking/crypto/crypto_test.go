package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
)

func TestNewSignedData(t *testing.T) {
	// Generate key pair
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Test data
	testData := struct {
		Message string `json:"message"`
		Number  int    `json:"number"`
	}{
		Message: "Hello, world!",
		Number:  42,
	}

	// Create signed data
	signedData, err := NewSignedData(testData, privateKey)
	if err != nil {
		t.Fatalf("Failed to create signed data: %v", err)
	}

	// Verify the signature
	if err := signedData.Verify(); err != nil {
		t.Errorf("Signature verification failed: %v", err)
	}

	// Verify the data
	if signedData.Data.Message != testData.Message {
		t.Errorf("Expected message %s, got %s", testData.Message, signedData.Data.Message)
	}

	if signedData.Data.Number != testData.Number {
		t.Errorf("Expected number %d, got %d", testData.Number, signedData.Data.Number)
	}

	// Verify public key was set correctly
	expectedPublicKey := privateKey.Public().(ed25519.PublicKey)
	if !signedData.PublicKey.Equal(expectedPublicKey) {
		t.Error("Public key mismatch")
	}
}

func TestVerify_InvalidSignature(t *testing.T) {
	// Generate two different key pairs
	_, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	// Create signed data with first key
	testData := "test message"
	signedData, err := NewSignedData(testData, privateKey1)
	if err != nil {
		t.Fatalf("Failed to create signed data: %v", err)
	}

	// Replace public key with second key (invalidating signature)
	signedData.PublicKey = privateKey2.Public().(ed25519.PublicKey)

	// Verification should fail
	if err := signedData.Verify(); err == nil {
		t.Error("Expected signature verification to fail, but it passed")
	}
}

func TestSignedDataFromBytes(t *testing.T) {
	// Generate key pair
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Test data
	testData := "test message"

	// Create signed data
	signedData, err := NewSignedData(testData, privateKey)
	if err != nil {
		t.Fatalf("Failed to create signed data: %v", err)
	}

	// Serialize to bytes
	bytes, err := json.Marshal(signedData)
	if err != nil {
		t.Fatalf("Failed to marshal signed data: %v", err)
	}

	// Deserialize from bytes
	deserializedData, err := SignedDataFromBytes[string](bytes)
	if err != nil {
		t.Fatalf("Failed to deserialize signed data: %v", err)
	}

	// Verify the deserialized data
	if err := deserializedData.Verify(); err != nil {
		t.Errorf("Deserialized signature verification failed: %v", err)
	}

	if deserializedData.Data != testData {
		t.Errorf("Expected data %s, got %s", testData, deserializedData.Data)
	}
}

func TestAuthenticatedSignedData(t *testing.T) {
	// Generate key pairs
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	// Test data
	testData := "test message"

	// Create signed data with first key
	signedData, err := NewSignedData(testData, privateKey1)
	if err != nil {
		t.Fatalf("Failed to create signed data: %v", err)
	}

	// Create authenticated signed data with correct expected key
	authData, err := NewAuthenticatedSignedData(signedData, publicKey1)
	if err != nil {
		t.Fatalf("Failed to create authenticated signed data: %v", err)
	}

	// Authentication should pass
	if err := authData.Authenticate(); err != nil {
		t.Errorf("Authentication failed: %v", err)
	}

	// Create authenticated signed data with wrong expected key
	wrongPublicKey := privateKey2.Public().(ed25519.PublicKey)
	_, err = NewAuthenticatedSignedData(signedData, wrongPublicKey)
	if err == nil {
		t.Error("Expected authentication to fail with wrong key, but it passed")
	}
}