package crdt

import (
	"crypto/ed25519"

	"github.com/hcp-uw/mosaic/internal/networking/protocol"
)

// EventType represents different types of events that can occur during CRDT operations
type EventType int

const (
	NewPeerDetected EventType = iota
	PeerUpdated
	ConflictResolved
	StoreOperationAdded
	StoreOperationUpdated
)

// Event represents an event that occurred during CRDT operations
type Event struct {
	Type       EventType
	NodeID     string
	PublicKey  ed25519.PublicKey
	FileHash   string
	Message    string
	OldValue   interface{}
	NewValue   interface{}
}

// CRDT represents a conflict-free replicated data type for peer network management
type CRDT struct {
	JoinMessages map[string]*protocol.SignedJoinMessage
	FileManifest map[string]*protocol.StoreOperationMessage
}

// Operations defines the interface for CRDT operations
type Operations interface {
	AddJoinMessage(nodeID string, signedJoinMsg *protocol.SignedJoinMessage) ([]Event, error)
	AddStoreOperation(fileHash string, storeOpMsg *protocol.StoreOperationMessage) ([]Event, error)
	Merge(other *CRDT) ([]Event, error)
	Validate() error
	Clone() *CRDT
	Equals(other *CRDT) bool
	GetJoinMessages() map[string]*protocol.SignedJoinMessage
	GetFileManifest() map[string]*protocol.StoreOperationMessage
}

// Ensure CRDT implements Operations
var _ Operations = (*CRDT)(nil)