package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crypto"
)

// createSetDataHandler creates a handler for PeerDataMessage (SetData requests)
func createSetDataHandler(privateKey ed25519.PrivateKey) func([]byte) ([]byte, error) {
	return func(rawMessage []byte) ([]byte, error) {
		// Parse the full message
		var message PeerMessage[*SignedStoreRequest]
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("failed to parse store request message: %w", err)
		}

		// Verify the store request signature
		if err := message.Data.Verify(); err != nil {
			return createStoreAcknowledge(message.Data.Data.DataHash, StoreRejected, privateKey)
		}

		// For now, accept all valid requests (in real implementation, check storage constraints)
		return createStoreAcknowledge(message.Data.Data.DataHash, StoreAccepted, privateKey)
	}
}

// createStoreAcknowledge creates a signed store acknowledgment response
func createStoreAcknowledge(requestHash []byte, status StoreStatus, privateKey ed25519.PrivateKey) ([]byte, error) {
	// Create the acknowledgment
	ack := StoreAcknowledge{
		RequestHash: requestHash,
		Status:      status,
		Timestamp:   time.Now(),
	}

	// Sign the acknowledgment
	signedAck, err := crypto.NewSignedData(ack, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign acknowledgment: %w", err)
	}

	// Serialize the response
	return json.Marshal(signedAck)
}

// createGetCRDTHandler creates a handler for PeerGetCRDTMessage (CRDT requests)
func createGetCRDTHandler(localCRDT interface{}) func([]byte) ([]byte, error) {
	return func(rawMessage []byte) ([]byte, error) {
		// Parse the full message
		var message PeerMessage[GetCRDTRequest]
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("failed to parse get CRDT request message: %w", err)
		}

		// Create CRDT response
		response := GetCRDTResponse{
			CRDT:      localCRDT,
			Timestamp: time.Now(),
		}

		// Serialize the response
		return json.Marshal(response)
	}
}

// CRDTMerger defines an interface for CRDT merge operations to avoid import cycles
type CRDTMerger interface {
	Merge(other interface{}) ([]interface{}, error)
}

// createMergeCRDTHandler creates a handler for PeerMergeCRDTMessage
func createMergeCRDTHandler(localCRDT interface{}) func([]byte) ([]byte, error) {
	return func(rawMessage []byte) ([]byte, error) {
		// Parse the full message
		var message PeerMessage[MergeCRDTRequest]
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("failed to parse merge CRDT request message: %w", err)
		}

		// Try to merge if localCRDT implements the merger interface
		var events []CRDTEvent
		if merger, ok := localCRDT.(CRDTMerger); ok {
			mergeEvents, err := merger.Merge(message.Data.CRDT)
			if err != nil {
				return nil, fmt.Errorf("failed to merge CRDT: %w", err)
			}
			// Convert to CRDTEvent interface
			for _, event := range mergeEvents {
				events = append(events, event)
			}
		}

		// Create response with events
		response := MergeCRDTResponse{
			Events:    events,
			Timestamp: time.Now(),
		}

		// Serialize the response
		return json.Marshal(response)
	}
}

// createGetPeerIPsHandler creates a handler for PeerGetIPsMessage
func createGetPeerIPsHandler() func([]byte) ([]byte, error) {
	return func(rawMessage []byte) ([]byte, error) {
		// Parse the full message
		var message PeerMessage[GetPeerIPsRequest]
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("failed to parse get peer IPs request message: %w", err)
		}

		// TODO: Get actual connected peer IPs from connection manager
		// For now, return empty list
		peerIPs := []NodeIP{}

		// Create response
		response := GetPeerIPsResponse{
			PeerIPs:   peerIPs,
			Timestamp: time.Now(),
		}

		// Serialize the response
		return json.Marshal(response)
	}
}

// CRDTKeyChecker defines an interface for checking if a key exists in CRDT
type CRDTKeyChecker interface {
	HasPublicKey(publicKey ed25519.PublicKey) bool
}

// createVerifyPeerKeyHandler creates a handler for PeerVerifyKeyMessage
func createVerifyPeerKeyHandler(localCRDT interface{}, privateKey ed25519.PrivateKey) func([]byte) ([]byte, error) {
	return func(rawMessage []byte) ([]byte, error) {
		// Parse the full message
		var message PeerMessage[VerifyPeerKeyRequest]
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, fmt.Errorf("failed to parse verify peer key request message: %w", err)
		}

		// Check if the public key matches our key
		ourPublicKey := privateKey.Public().(ed25519.PublicKey)
		isOurKey := bytes.Equal(message.Data.PublicKey, ourPublicKey)

		// Check if the key is in our CRDT using the interface
		inCRDT := false
		if checker, ok := localCRDT.(CRDTKeyChecker); ok {
			inCRDT = checker.HasPublicKey(message.Data.PublicKey)
		}

		// Create response
		response := VerifyPeerKeyResponse{
			IsValid:   isOurKey,
			InCRDT:    inCRDT,
			Timestamp: time.Now(),
		}

		// Serialize the response
		return json.Marshal(response)
	}
}