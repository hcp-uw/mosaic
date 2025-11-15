package crypto

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SignedData wraps any data type with a signature
type SignedData[T any] struct {
	Data      T
	PublicKey ed25519.PublicKey
	Signature []byte
	Timestamp time.Time
}

// NewSignedData creates and signs data
func NewSignedData[T any](data T, privateKey ed25519.PrivateKey) (*SignedData[T], error) {
	publicKey := privateKey.Public().(ed25519.PublicKey)

	// Serialize the data for signing
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	timestamp := time.Now()

	// Create message to sign (data + timestamp)
	message := append(dataBytes, []byte(timestamp.Format(time.RFC3339Nano))...)

	// Sign the message
	signature := ed25519.Sign(privateKey, message)

	return &SignedData[T]{
		Data:      data,
		PublicKey: publicKey,
		Signature: signature,
		Timestamp: timestamp,
	}, nil
}

// FromBytes deserializes and verifies the signature matches the data type
func SignedDataFromBytes[T any](data []byte) (*SignedData[T], error) {
	var signed SignedData[T]
	if err := json.Unmarshal(data, &signed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal signed data: %w", err)
	}

	// Verify the data type matches T
	if _, err := json.Marshal(signed.Data); err != nil {
		return nil, fmt.Errorf("data type mismatch: %w", err)
	}

	return &signed, nil
}

// Verify checks that the signature is valid
func (s *SignedData[T]) Verify() error {
	dataBytes, err := json.Marshal(s.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal data for verification: %w", err)
	}

	message := append(dataBytes, []byte(s.Timestamp.Format(time.RFC3339Nano))...)

	if !ed25519.Verify(s.PublicKey, message, s.Signature) {
		return errors.New("invalid signature")
	}

	return nil
}

// AuthenticatedSignedData wraps SignedData and requires a specific public key
type AuthenticatedSignedData[T any] struct {
	*SignedData[T]
	ExpectedPublicKey ed25519.PublicKey
}

// NewAuthenticatedSignedData creates authenticated signed data from raw SignedData
func NewAuthenticatedSignedData[T any](signed *SignedData[T], expectedPublicKey ed25519.PublicKey) (*AuthenticatedSignedData[T], error) {
	auth := &AuthenticatedSignedData[T]{
		SignedData:        signed,
		ExpectedPublicKey: expectedPublicKey,
	}

	if err := auth.Authenticate(); err != nil {
		return nil, err
	}

	return auth, nil
}

// Authenticate verifies both the signature AND that it's from the expected key
func (a *AuthenticatedSignedData[T]) Authenticate() error {
	// First verify the signature is valid
	if err := a.Verify(); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// Then verify it's from the expected public key
	if !a.PublicKey.Equal(a.ExpectedPublicKey) {
		return fmt.Errorf("public key mismatch: expected %x, got %x",
			a.ExpectedPublicKey, a.PublicKey)
	}

	return nil
}