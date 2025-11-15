package crdt

import (
	"bytes"
	"crypto/ed25519"
	"fmt"

	"github.com/hcp-uw/mosaic/internal/networking/protocol"
)

// New creates a new initialized CRDT
func New() *CRDT {
	return &CRDT{
		JoinMessages: make(map[string]*protocol.SignedJoinMessage),
		FileManifest: make(map[string]*protocol.StoreOperationMessage),
	}
}

// AddJoinMessage adds a new join message to the CRDT with conflict resolution
func (c *CRDT) AddJoinMessage(nodeID string, signedJoinMsg *protocol.SignedJoinMessage) ([]Event, error) {
	var events []Event

	if signedJoinMsg == nil {
		return events, fmt.Errorf("signed join message cannot be nil")
	}

	// Verify the signature before adding
	if err := signedJoinMsg.Verify(); err != nil {
		return events, fmt.Errorf("join message signature verification failed: %w", err)
	}

	// Verify the nodeID matches the message data
	if signedJoinMsg.Data.NodeID != nodeID {
		return events, fmt.Errorf("nodeID mismatch: expected %s, got %s", nodeID, signedJoinMsg.Data.NodeID)
	}

	// Check if we already have a join message for this node
	if existing, exists := c.JoinMessages[nodeID]; exists {
		// Conflict resolution: use the message with the later timestamp
		if signedJoinMsg.Timestamp.After(existing.Timestamp) {
			events = append(events, Event{
				Type:      ConflictResolved,
				NodeID:    nodeID,
				PublicKey: signedJoinMsg.Data.PublicKey,
				Message:   "Newer join message replaced older one",
				OldValue:  existing,
				NewValue:  signedJoinMsg,
			})
			c.JoinMessages[nodeID] = signedJoinMsg
		}
		// If timestamps are equal, use deterministic ordering (e.g., by signature bytes)
		// This ensures all nodes make the same decision
		if signedJoinMsg.Timestamp.Equal(existing.Timestamp) {
			if compareSignatures(signedJoinMsg.Signature, existing.Signature) > 0 {
				events = append(events, Event{
					Type:      ConflictResolved,
					NodeID:    nodeID,
					PublicKey: signedJoinMsg.Data.PublicKey,
					Message:   "Deterministic conflict resolution based on signature",
					OldValue:  existing,
					NewValue:  signedJoinMsg,
				})
				c.JoinMessages[nodeID] = signedJoinMsg
			}
		}
	} else {
		// No existing message, add directly - this is a new peer
		c.JoinMessages[nodeID] = signedJoinMsg
		events = append(events, Event{
			Type:      NewPeerDetected,
			NodeID:    nodeID,
			PublicKey: signedJoinMsg.Data.PublicKey,
			Message:   "New peer joined the network",
			NewValue:  signedJoinMsg,
		})
	}

	return events, nil
}

// AddStoreOperation adds a new store operation to the CRDT with conflict resolution
func (c *CRDT) AddStoreOperation(fileHash string, storeOpMsg *protocol.StoreOperationMessage) ([]Event, error) {
	var events []Event

	if storeOpMsg == nil {
		return events, fmt.Errorf("store operation message cannot be nil")
	}

	// Verify both the request and acknowledge signatures
	if storeOpMsg.SignedStoreRequest != nil {
		if err := storeOpMsg.SignedStoreRequest.Verify(); err != nil {
			return events, fmt.Errorf("store request signature verification failed: %w", err)
		}
	}

	if storeOpMsg.SignedStoreAcknowledge != nil {
		if err := storeOpMsg.SignedStoreAcknowledge.Verify(); err != nil {
			return events, fmt.Errorf("store acknowledge signature verification failed: %w", err)
		}
	}

	// Verify the file hash matches if we have a store request
	if storeOpMsg.SignedStoreRequest != nil {
		requestHash := fmt.Sprintf("%x", storeOpMsg.SignedStoreRequest.Data.DataHash)
		if requestHash != fileHash {
			return events, fmt.Errorf("file hash mismatch: expected %s, got %s", fileHash, requestHash)
		}
	}

	// Check if we already have a store operation for this file
	if existing, exists := c.FileManifest[fileHash]; exists {
		// Conflict resolution: merge the operations, keeping the latest acknowledge
		mergedOp := &protocol.StoreOperationMessage{
			SignedStoreRequest:     existing.SignedStoreRequest,
			SignedStoreAcknowledge: existing.SignedStoreAcknowledge,
		}

		// Update request if the new one is more recent
		if storeOpMsg.SignedStoreRequest != nil {
			if existing.SignedStoreRequest == nil ||
				storeOpMsg.SignedStoreRequest.Timestamp.After(existing.SignedStoreRequest.Timestamp) {
				mergedOp.SignedStoreRequest = storeOpMsg.SignedStoreRequest
				events = append(events, Event{
					Type:     StoreOperationUpdated,
					FileHash: fileHash,
					Message:  "Store request updated with newer version",
					OldValue: existing.SignedStoreRequest,
					NewValue: storeOpMsg.SignedStoreRequest,
				})
			}
		}

		// Update acknowledge if the new one is more recent
		if storeOpMsg.SignedStoreAcknowledge != nil {
			if existing.SignedStoreAcknowledge == nil ||
				storeOpMsg.SignedStoreAcknowledge.Timestamp.After(existing.SignedStoreAcknowledge.Timestamp) {
				mergedOp.SignedStoreAcknowledge = storeOpMsg.SignedStoreAcknowledge
				events = append(events, Event{
					Type:     StoreOperationUpdated,
					FileHash: fileHash,
					Message:  "Store acknowledge updated with newer version",
					OldValue: existing.SignedStoreAcknowledge,
					NewValue: storeOpMsg.SignedStoreAcknowledge,
				})
			}
		}

		c.FileManifest[fileHash] = mergedOp
	} else {
		// No existing operation, add directly
		c.FileManifest[fileHash] = storeOpMsg
		events = append(events, Event{
			Type:     StoreOperationAdded,
			FileHash: fileHash,
			Message:  "New store operation added",
			NewValue: storeOpMsg,
		})
	}

	return events, nil
}

// Merge merges another CRDT into this one, resolving conflicts
func (c *CRDT) Merge(other *CRDT) ([]Event, error) {
	var allEvents []Event

	if other == nil {
		return allEvents, fmt.Errorf("cannot merge with nil CRDT")
	}

	// Merge join messages
	for nodeID, joinMsg := range other.JoinMessages {
		events, err := c.AddJoinMessage(nodeID, joinMsg)
		if err != nil {
			return allEvents, fmt.Errorf("failed to merge join message for node %s: %w", nodeID, err)
		}
		allEvents = append(allEvents, events...)
	}

	// Merge file manifest
	for fileHash, storeOp := range other.FileManifest {
		events, err := c.AddStoreOperation(fileHash, storeOp)
		if err != nil {
			return allEvents, fmt.Errorf("failed to merge store operation for file %s: %w", fileHash, err)
		}
		allEvents = append(allEvents, events...)
	}

	return allEvents, nil
}

// Validate verifies the integrity of all data in the CRDT
func (c *CRDT) Validate() error {
	// Validate all join messages
	for nodeID, joinMsg := range c.JoinMessages {
		if joinMsg == nil {
			return fmt.Errorf("nil join message for node %s", nodeID)
		}

		if err := joinMsg.Verify(); err != nil {
			return fmt.Errorf("invalid join message signature for node %s: %w", nodeID, err)
		}

		if joinMsg.Data.NodeID != nodeID {
			return fmt.Errorf("nodeID mismatch for node %s: expected %s, got %s",
				nodeID, nodeID, joinMsg.Data.NodeID)
		}
	}

	// Validate all store operations
	for fileHash, storeOp := range c.FileManifest {
		if storeOp == nil {
			return fmt.Errorf("nil store operation for file %s", fileHash)
		}

		if storeOp.SignedStoreRequest != nil {
			if err := storeOp.SignedStoreRequest.Verify(); err != nil {
				return fmt.Errorf("invalid store request signature for file %s: %w", fileHash, err)
			}

			// Verify file hash matches
			requestHash := fmt.Sprintf("%x", storeOp.SignedStoreRequest.Data.DataHash)
			if requestHash != fileHash {
				return fmt.Errorf("file hash mismatch for file %s: expected %s, got %s",
					fileHash, fileHash, requestHash)
			}
		}

		if storeOp.SignedStoreAcknowledge != nil {
			if err := storeOp.SignedStoreAcknowledge.Verify(); err != nil {
				return fmt.Errorf("invalid store acknowledge signature for file %s: %w", fileHash, err)
			}
		}
	}

	return nil
}

// Clone creates a deep copy of the CRDT
func (c *CRDT) Clone() *CRDT {
	clone := New()

	// Clone join messages
	for nodeID, joinMsg := range c.JoinMessages {
		// Create a copy of the signed join message
		clonedJoinMsg := &protocol.SignedJoinMessage{
			Data:      joinMsg.Data, // JoinMessage is a value type, so this is safe
			PublicKey: make([]byte, len(joinMsg.PublicKey)),
			Signature: make([]byte, len(joinMsg.Signature)),
			Timestamp: joinMsg.Timestamp,
		}
		copy(clonedJoinMsg.PublicKey, joinMsg.PublicKey)
		copy(clonedJoinMsg.Signature, joinMsg.Signature)

		// PublicKey field also needs copying
		clonedJoinMsg.Data.PublicKey = make([]byte, len(joinMsg.Data.PublicKey))
		copy(clonedJoinMsg.Data.PublicKey, joinMsg.Data.PublicKey)

		clone.JoinMessages[nodeID] = clonedJoinMsg
	}

	// Clone file manifest
	for fileHash, storeOp := range c.FileManifest {
		clonedStoreOp := &protocol.StoreOperationMessage{}

		// Clone store request if it exists
		if storeOp.SignedStoreRequest != nil {
			req := storeOp.SignedStoreRequest
			clonedReq := &protocol.SignedStoreRequest{
				Data: protocol.StoreRequest{
					DataHash:  make([]byte, len(req.Data.DataHash)),
					Size:      req.Data.Size,
					Timestamp: req.Data.Timestamp,
				},
				PublicKey: make([]byte, len(req.PublicKey)),
				Signature: make([]byte, len(req.Signature)),
				Timestamp: req.Timestamp,
			}
			copy(clonedReq.Data.DataHash, req.Data.DataHash)
			copy(clonedReq.PublicKey, req.PublicKey)
			copy(clonedReq.Signature, req.Signature)
			clonedStoreOp.SignedStoreRequest = clonedReq
		}

		// Clone store acknowledge if it exists
		if storeOp.SignedStoreAcknowledge != nil {
			ack := storeOp.SignedStoreAcknowledge
			clonedAck := &protocol.SignedStoreAcknowledge{
				Data: protocol.StoreAcknowledge{
					RequestHash: make([]byte, len(ack.Data.RequestHash)),
					Status:      ack.Data.Status,
					Timestamp:   ack.Data.Timestamp,
				},
				PublicKey: make([]byte, len(ack.PublicKey)),
				Signature: make([]byte, len(ack.Signature)),
				Timestamp: ack.Timestamp,
			}
			copy(clonedAck.Data.RequestHash, ack.Data.RequestHash)
			copy(clonedAck.PublicKey, ack.PublicKey)
			copy(clonedAck.Signature, ack.Signature)
			clonedStoreOp.SignedStoreAcknowledge = clonedAck
		}

		clone.FileManifest[fileHash] = clonedStoreOp
	}

	return clone
}

// Equals compares two CRDTs for equality
func (c *CRDT) Equals(other *CRDT) bool {
	if other == nil {
		return false
	}

	// Compare join messages
	if len(c.JoinMessages) != len(other.JoinMessages) {
		return false
	}

	for nodeID, joinMsg := range c.JoinMessages {
		otherJoinMsg, exists := other.JoinMessages[nodeID]
		if !exists {
			return false
		}

		if !joinMessagesEqual(joinMsg, otherJoinMsg) {
			return false
		}
	}

	// Compare file manifest
	if len(c.FileManifest) != len(other.FileManifest) {
		return false
	}

	for fileHash, storeOp := range c.FileManifest {
		otherStoreOp, exists := other.FileManifest[fileHash]
		if !exists {
			return false
		}

		if !storeOperationsEqual(storeOp, otherStoreOp) {
			return false
		}
	}

	return true
}

// GetJoinMessages returns a copy of the join messages map
func (c *CRDT) GetJoinMessages() map[string]*protocol.SignedJoinMessage {
	result := make(map[string]*protocol.SignedJoinMessage, len(c.JoinMessages))
	for k, v := range c.JoinMessages {
		result[k] = v
	}
	return result
}

// GetFileManifest returns a copy of the file manifest map
func (c *CRDT) GetFileManifest() map[string]*protocol.StoreOperationMessage {
	result := make(map[string]*protocol.StoreOperationMessage, len(c.FileManifest))
	for k, v := range c.FileManifest {
		result[k] = v
	}
	return result
}

// HasPublicKey checks if a public key exists in the CRDT (implements protocol.CRDTKeyChecker)
func (c *CRDT) HasPublicKey(publicKey ed25519.PublicKey) bool {
	for _, joinMsg := range c.JoinMessages {
		if bytes.Equal(joinMsg.Data.PublicKey, publicKey) {
			return true
		}
	}
	return false
}