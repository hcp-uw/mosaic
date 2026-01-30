package api

import (
	"encoding/json"
	"errors"
	"net"
	"time"
)

// MessageType represents different STUN message types
type MessageType string

const (
	// Client to Server messages
	ClientRegister MessageType = "client_register"
	ClientPing     MessageType = "client_ping"

	// Server to Client messages
	PeerAssignment MessageType = "peer_assignment"
	ServerError    MessageType = "server_error"
	WaitingForPeer MessageType = "waiting_for_peer"

	// Peer to Peer messages
	PeerPing MessageType = "peer_ping"
	PeerPong MessageType = "peer_pong"
)

// Message represents the base message structure
type Message struct {
	Sign Signature `json:"sign"`

	Type      MessageType `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      any         `json:"data,omitempty"`
}

type Signature struct {
	PubKey string `json:"pub_key"`
}

func NewSignature(pubKey string) Signature {
	return Signature{PubKey: pubKey}
}

// ClientRegisterData represents client registration information (no data needed)
type ClientRegisterData struct {
	// No fields needed - client ID is derived from network address
}

// PeerAssignmentData contains peer connection information
type PeerAssignmentData struct {
	PeerAddress string `json:"peer_address"`
	PeerID      string `json:"peer_id"`
}

// ServerErrorData contains error information
type ServerErrorData struct {
	ErrorMessage string `json:"error_message"`
	ErrorCode    string `json:"error_code"`
}

// PeerPingData contains peer ping information
type PeerPingData struct {
	Timestamp time.Time `json:"timestamp"`
}

// NewClientRegisterMessage creates a client registration message
func NewClientRegisterMessage() *Message {
	return &Message{
		Type:      ClientRegister,
		Timestamp: time.Now(),
		Data:      ClientRegisterData{},
	}
}

// NewPeerAssignmentMessage creates a peer assignment message
func NewPeerAssignmentMessage(peerAddr *net.UDPAddr, peerID string) *Message {
	return &Message{
		Type:      PeerAssignment,
		Timestamp: time.Now(),
		Data: PeerAssignmentData{
			PeerAddress: peerAddr.String(),
			PeerID:      peerID,
		},
	}
}

// NewWaitingForPeerMessage creates a waiting message
func NewWaitingForPeerMessage() *Message {
	return &Message{
		Type:      WaitingForPeer,
		Timestamp: time.Now(),
	}
}

// NewServerErrorMessage creates an error message
func NewServerErrorMessage(errorMsg, errorCode string) *Message {
	return &Message{
		Type:      ServerError,
		Timestamp: time.Now(),
		Data: ServerErrorData{
			ErrorMessage: errorMsg,
			ErrorCode:    errorCode,
		},
	}
}

// NewClientPingMessage creates a ping message
func NewClientPingMessage(sign Signature) *Message {
	return &Message{
		Sign:      sign,
		Type:      ClientPing,
		Timestamp: time.Now(),
		Data:      ClientRegisterData{},
	}
}

// NewPeerPingMessage creates a peer ping message
func NewPeerPingMessage(sign Signature) *Message {
	return &Message{
		Sign:      sign,
		Type:      PeerPing,
		Timestamp: time.Now(),
		Data: PeerPingData{
			Timestamp: time.Now(),
		},
	}
}

// NewPeerPongMessage creates a peer pong response message
func NewPeerPongMessage(sign Signature) *Message {
	return &Message{
		Sign:      sign,
		Type:      PeerPong,
		Timestamp: time.Now(),
		Data: PeerPingData{
			Timestamp: time.Now(),
		},
	}
}

// Serialize converts a message to JSON bytes
func (m *Message) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// DeserializeMessage converts JSON bytes to a message
func DeserializeMessage(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	return &msg, err
}

// GetClientRegisterData extracts client registration data from message
func (m *Message) GetClientRegisterData() (*ClientRegisterData, error) {
	if m.Type != ClientRegister && m.Type != ClientPing {
		return nil, ErrInvalidMessageType
	}

	// No data validation needed since ClientRegisterData is empty
	return &ClientRegisterData{}, nil
}

// GetPeerAssignmentData extracts peer assignment data from message
func (m *Message) GetPeerAssignmentData() (*PeerAssignmentData, error) {
	if m.Type != PeerAssignment {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data PeerAssignmentData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// GetServerErrorData extracts error data from message
func (m *Message) GetServerErrorData() (*ServerErrorData, error) {
	if m.Type != ServerError {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data ServerErrorData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// GetPeerPingData extracts peer ping data from message
func (m *Message) GetPeerPingData() (*PeerPingData, error) {
	if m.Type != PeerPing && m.Type != PeerPong {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data PeerPingData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// GetPeerPongData extracts peer pong data from message
func (m *Message) GetPeerPongData() (*PeerPingData, error) {
	if m.Type != PeerPing && m.Type != PeerPong {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data PeerPingData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// Error types
var (
	ErrInvalidMessageType = errors.New("invalid message type")
)
