package protocol

import (
	"crypto/ed25519"
	"net"
	"time"

	"github.com/hcp-uw/mosaic/internal/networking/crypto"
)

// NodeIP represents a network node IP address
type NodeIP net.IP

// StoreStatus represents the status of a store operation
type StoreStatus int

const (
	StoreAccepted StoreStatus = iota
	StoreRejected
	StorePartial
	StorePending
)

// Core message types

// JoinMessage represents a network join request
type JoinMessage struct {
	NodeID    string
	PublicKey ed25519.PublicKey
}

// StoreRequest represents a request to store data
type StoreRequest struct {
	DataHash  []byte
	Size      int64
	Timestamp time.Time
}

// StoreAcknowledge represents an acknowledgment of a store request
type StoreAcknowledge struct {
	RequestHash []byte
	Status      StoreStatus
	Timestamp   time.Time
}

// GetCRDTRequest represents a request to get CRDT data
type GetCRDTRequest struct {
	Timestamp time.Time
}

// Forward declarations - will be resolved by imports
type CRDT interface{}
type CRDTEvent interface{}

// GetCRDTResponse represents a response containing CRDT data
type GetCRDTResponse struct {
	CRDT      CRDT
	Timestamp time.Time
}

// MergeCRDTRequest represents a request to merge CRDT data
type MergeCRDTRequest struct {
	CRDT      CRDT
	Timestamp time.Time
}

// MergeCRDTResponse represents a response from CRDT merge operation
type MergeCRDTResponse struct {
	Events    []CRDTEvent
	Timestamp time.Time
}

// GetPeerIPsRequest represents a request for peer IP addresses
type GetPeerIPsRequest struct {
	Timestamp time.Time
}

// GetPeerIPsResponse represents a response containing peer IP addresses
type GetPeerIPsResponse struct {
	PeerIPs   []NodeIP
	Timestamp time.Time
}

// VerifyPeerKeyRequest represents a request to verify a peer's public key
type VerifyPeerKeyRequest struct {
	PublicKey ed25519.PublicKey
	Timestamp time.Time
}

// VerifyPeerKeyResponse represents a response from peer key verification
type VerifyPeerKeyResponse struct {
	IsValid   bool
	InCRDT    bool
	Timestamp time.Time
}

// Signed message type aliases
type SignedJoinMessage = crypto.SignedData[JoinMessage]
type SignedStoreRequest = crypto.SignedData[StoreRequest]
type SignedStoreAcknowledge = crypto.SignedData[StoreAcknowledge]

// Convenience constructors for signed data
func NewSignedJoinMessage(data JoinMessage, privateKey ed25519.PrivateKey) (*SignedJoinMessage, error) {
	return crypto.NewSignedData(data, privateKey)
}

func NewSignedStoreRequest(data StoreRequest, privateKey ed25519.PrivateKey) (*SignedStoreRequest, error) {
	return crypto.NewSignedData(data, privateKey)
}

func NewSignedStoreAcknowledge(data StoreAcknowledge, privateKey ed25519.PrivateKey) (*SignedStoreAcknowledge, error) {
	return crypto.NewSignedData(data, privateKey)
}

// StoreOperationMessage represents a complete store operation with request and acknowledgment
type StoreOperationMessage struct {
	SignedStoreRequest     *SignedStoreRequest
	SignedStoreAcknowledge *SignedStoreAcknowledge
}

// Message type enumerations

// StunMessageCode represents STUN server message types
type StunMessageCode int

const (
	JoinNetworkMessage StunMessageCode = iota
	GetCRDTMessage
	GetPeerIPsMessage
)

// PeerMessageCode represents peer-to-peer message types
type PeerMessageCode int

const (
	PeerConnectMessage PeerMessageCode = iota
	PeerHandshakeMessage
	PeerDataMessage
	PeerGetCRDTMessage
	PeerMergeCRDTMessage
	PeerGetIPsMessage
	PeerVerifyKeyMessage
)

// Message wrappers for network transmission

// StunMessage represents a message sent to a STUN server
type StunMessage struct {
	MessageType StunMessageCode `json:"message_type"`
	Timestamp   int64           `json:"timestamp"`
}

// PeerMessage represents a generic peer-to-peer message
type PeerMessage[T any] struct {
	MessageType PeerMessageCode `json:"message_type"`
	Data        T               `json:"data"`
	Timestamp   int64           `json:"timestamp"`
}