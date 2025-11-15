package protocol

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
)

// MessageRouter handles incoming peer messages and routes them to appropriate handlers
type MessageRouter struct {
	handlers map[PeerMessageCode]func([]byte) ([]byte, error)
}

// NewMessageRouter creates a new message router with default handlers
func NewMessageRouter(privateKey ed25519.PrivateKey) *MessageRouter {
	router := &MessageRouter{
		handlers: make(map[PeerMessageCode]func([]byte) ([]byte, error)),
	}

	// Register default handlers
	router.RegisterHandler(PeerDataMessage, createSetDataHandler(privateKey))

	return router
}

// NewMessageRouterWithCRDT creates a new message router with CRDT-specific handlers
func NewMessageRouterWithCRDT(privateKey ed25519.PrivateKey, localCRDT interface{}) *MessageRouter {
	router := &MessageRouter{
		handlers: make(map[PeerMessageCode]func([]byte) ([]byte, error)),
	}

	// Register all handlers
	router.RegisterHandler(PeerDataMessage, createSetDataHandler(privateKey))
	router.RegisterHandler(PeerGetCRDTMessage, createGetCRDTHandler(localCRDT))
	router.RegisterHandler(PeerMergeCRDTMessage, createMergeCRDTHandler(localCRDT))
	router.RegisterHandler(PeerGetIPsMessage, createGetPeerIPsHandler())
	router.RegisterHandler(PeerVerifyKeyMessage, createVerifyPeerKeyHandler(localCRDT, privateKey))

	return router
}

// RegisterHandler registers a handler for a specific message type
func (r *MessageRouter) RegisterHandler(messageType PeerMessageCode, handler func([]byte) ([]byte, error)) {
	r.handlers[messageType] = handler
}

// RouteMessage processes an incoming message and returns the response
func (r *MessageRouter) RouteMessage(rawMessage []byte) ([]byte, error) {
	// First, parse just the message type to determine routing
	var messageHeader struct {
		MessageType PeerMessageCode `json:"message_type"`
	}

	if err := json.Unmarshal(rawMessage, &messageHeader); err != nil {
		return nil, fmt.Errorf("failed to parse message header: %w", err)
	}

	// Find the appropriate handler
	handler, exists := r.handlers[messageHeader.MessageType]
	if !exists {
		return nil, fmt.Errorf("no handler registered for message type: %d", messageHeader.MessageType)
	}

	// Route to the specific handler
	return handler(rawMessage)
}