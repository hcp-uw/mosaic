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
	RegisterSuccess  MessageType = "register_success"
	PeerAssignment   MessageType = "peer_assignment"
	ServerError      MessageType = "server_error"
	WaitingForPeer   MessageType = "waiting_for_peer"
	AssignedAsLeader MessageType = "assigned_as_leader"

	// Leader to Peer message
	// To be sent to the joining node contianing a list of all nodes in the network
	CurrentMembers MessageType = "current_members"
	// To be sent to the nodes in the network notifying them of the new node that is joining
	NewPeerJoiner MessageType = "new_joiner"

	// Peer to Peer messages
	PeerPing        MessageType = "peer_ping"
	PeerPong        MessageType = "peer_pong"
	PeerTextMessage MessageType = "peer_text_message"
	ManifestSync    MessageType = "manifest_sync"
	ShardPush       MessageType = "shard_push"
	ShardRequest    MessageType = "shard_request"
	ShardResponse   MessageType = "shard_response"
	ShardChunk      MessageType = "shard_chunk"
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

// ClientRegisterData represents client registration information.
type ClientRegisterData struct {
	Token string `json:"token"` // JWT from auth server; verified by STUN before pairing
}

type RegisterSuccessData struct {
	Message       string `json:"message"`
	ID            string `json:"id"`
	QueuePosition int    `json:"queuePosition"` // server-assigned; 1 = leader, 2 = next, etc.
}

// Doesnt need to contain anything
// This message is sent to the first node to connect to the server tell them that they are
// the leader and must maintain a connection to the server
type ServerAssignedLeaderData struct {
}

// Dictionary of nodeID's and there respective UDPAddr
type CurrentMembersData struct {
	Members map[string]string `json:"members"`
}

type NewPeerJoinerData struct {
	JoinerAddress string `json:"joiner_address"`
	JoinerID      string `json:"joiner_id"`
}

// just for testing rn -sending text messages between terminals
type PeerTextMessageData struct {
	Message string `json:"message"`
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

func NewPeerTextMessage(message, senderID string) *Message {
	return &Message{
		Type:      PeerTextMessage,
		Timestamp: time.Now(),
		Sign: Signature{
			PubKey: senderID,
		},
		Data: PeerTextMessageData{
			Message: message,
		},
	}
}

func NewNewPeerJoinerMessage(senderID, joinerID, joinerAddr string) *Message {
	return &Message{
		Type:      NewPeerJoiner,
		Timestamp: time.Now(),
		Sign:      NewSignature(senderID),
		Data: NewPeerJoinerData{
			JoinerAddress: joinerAddr,
			JoinerID:      joinerID,
		},
	}
}

func NewCurrentMembersMessage(members map[string]*net.UDPAddr, senderID string) *Message {
	stringMembers := make(map[string]string)

	// 2. Iterate and convert each UDPAddr to a string
	for id, addr := range members {
		if addr != nil {
			stringMembers[id] = addr.String()
		}
	}

	return &Message{
		Type:      CurrentMembers,
		Timestamp: time.Now(),
		Sign: Signature{
			PubKey: senderID,
		},
		Data: CurrentMembersData{
			Members: stringMembers,
		},
	}

}

func NewServerAssignedLeaderMessage() *Message {
	return &Message{
		Type:      AssignedAsLeader,
		Timestamp: time.Now(),
		Data:      ServerAssignedLeaderData{},
	}
}

// NewClientRegisterMessage creates a client registration message with the auth token.
func NewClientRegisterMessage(token string) *Message {
	return &Message{
		Type:      ClientRegister,
		Timestamp: time.Now(),
		Data:      ClientRegisterData{Token: token},
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

func NewRegisterSuccessMessage(message, id string, queuePosition int) *Message {
	return &Message{
		Type:      RegisterSuccess,
		Timestamp: time.Now(),
		Data: RegisterSuccessData{
			Message:       message,
			ID:            id,
			QueuePosition: queuePosition,
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
	b, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var d ClientRegisterData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (m *Message) GetCurrentMembersData() (*CurrentMembersData, error) {
	if m.Type != CurrentMembers {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data CurrentMembersData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

func (m *Message) GetNewPeerJoinerData() (*NewPeerJoinerData, error) {
	if m.Type != NewPeerJoiner {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data NewPeerJoinerData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

func (m *Message) GetPeerTextMessageData() (*PeerTextMessageData, error) {
	if m.Type != PeerTextMessage {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data PeerTextMessageData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

func (m *Message) GetAssignedAsLeaderData() (*ServerAssignedLeaderData, error) {
	if m.Type != AssignedAsLeader {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data ServerAssignedLeaderData
	err = json.Unmarshal(dataBytes, &data)
	if err != nil {
		return nil, err
	}

	return &data, err
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

func (m *Message) GetRegisterSuccessData() (*RegisterSuccessData, error) {
	if m.Type != RegisterSuccess {
		return nil, ErrInvalidMessageType
	}

	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}

	var data RegisterSuccessData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// ManifestSyncData carries the full serialized network manifest (JSON, not
// encrypted — each UserNetworkEntry's Files section is already ECIES-encrypted
// and the outer envelope is just the manifest index). Transit security comes
// from the per-entry ECDSA signatures; a tampered payload is rejected on merge.
type ManifestSyncData struct {
	ManifestJSON []byte `json:"manifestJSON"`
}

// NewManifestSyncMessage creates a manifest sync message for P2P broadcast.
func NewManifestSyncMessage(manifestJSON []byte) *Message {
	return &Message{
		Type:      ManifestSync,
		Timestamp: time.Now(),
		Data:      ManifestSyncData{ManifestJSON: manifestJSON},
	}
}

// GetManifestSyncData extracts manifest sync data from a message.
func (m *Message) GetManifestSyncData() (*ManifestSyncData, error) {
	if m.Type != ManifestSync {
		return nil, ErrInvalidMessageType
	}
	dataBytes, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var data ManifestSyncData
	err = json.Unmarshal(dataBytes, &data)
	return &data, err
}

// ShardPushData carries a single Reed-Solomon shard from an uploading peer.
type ShardPushData struct {
	FileHash        string `json:"fileHash"`        // hex SHA-256 of original file
	FileName        string `json:"fileName"`        // base name without extension
	FileSize        int    `json:"fileSize"`        // original file size in bytes (needed by DecodeShards)
	ShardIndex      int    `json:"shardIndex"`      // 0-based index of this shard
	TotalDataShards int    `json:"totalDataShards"` // minimum shards needed to reconstruct
	TotalShards     int    `json:"totalShards"`     // data + parity
	Data            []byte `json:"data"`            // raw shard bytes
}

// ShardRequestData asks a peer to send a specific shard.
type ShardRequestData struct {
	FileHash   string `json:"fileHash"`
	ShardIndex int    `json:"shardIndex"`
}

// ShardResponseData is the reply to a ShardRequest.
type ShardResponseData struct {
	FileHash   string `json:"fileHash"`
	ShardIndex int    `json:"shardIndex"`
	Found      bool   `json:"found"`
	Data       []byte `json:"data,omitempty"`
}

func NewShardPushMessage(sign Signature, d ShardPushData) *Message {
	return &Message{
		Sign:      sign,
		Type:      ShardPush,
		Timestamp: time.Now(),
		Data:      d,
	}
}

func NewShardRequestMessage(sign Signature, d ShardRequestData) *Message {
	return &Message{
		Sign:      sign,
		Type:      ShardRequest,
		Timestamp: time.Now(),
		Data:      d,
	}
}

func NewShardResponseMessage(sign Signature, d ShardResponseData) *Message {
	return &Message{
		Sign:      sign,
		Type:      ShardResponse,
		Timestamp: time.Now(),
		Data:      d,
	}
}

func (m *Message) GetShardPushData() (*ShardPushData, error) {
	if m.Type != ShardPush {
		return nil, ErrInvalidMessageType
	}
	b, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var d ShardPushData
	err = json.Unmarshal(b, &d)
	return &d, err
}

func (m *Message) GetShardRequestData() (*ShardRequestData, error) {
	if m.Type != ShardRequest {
		return nil, ErrInvalidMessageType
	}
	b, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var d ShardRequestData
	err = json.Unmarshal(b, &d)
	return &d, err
}

func (m *Message) GetShardResponseData() (*ShardResponseData, error) {
	if m.Type != ShardResponse {
		return nil, ErrInvalidMessageType
	}
	b, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var d ShardResponseData
	err = json.Unmarshal(b, &d)
	return &d, err
}

// ShardChunkData carries one 32KB slice of a Reed-Solomon shard for large-file transfer.
// Shards are split into chunks because a full shard (e.g. 100MB) far exceeds the UDP max.
type ShardChunkData struct {
	FileHash        string `json:"fileHash"`
	FileName        string `json:"fileName"`        // base name, no extension
	FileSize        int    `json:"fileSize"`        // original file size in bytes
	ShardIndex      int    `json:"shardIndex"`      // 0-based shard number
	ChunkIndex      int    `json:"chunkIndex"`      // 0-based chunk within this shard
	TotalChunks     int    `json:"totalChunks"`     // total chunks for this shard
	TotalDataShards int    `json:"totalDataShards"` // data shards needed to reconstruct
	TotalShards     int    `json:"totalShards"`     // data + parity
	Data            []byte `json:"data"`            // ≤32KB raw bytes
}

func NewShardChunkMessage(sign Signature, d ShardChunkData) *Message {
	return &Message{
		Sign:      sign,
		Type:      ShardChunk,
		Timestamp: time.Now(),
		Data:      d,
	}
}

func (m *Message) GetShardChunkData() (*ShardChunkData, error) {
	if m.Type != ShardChunk {
		return nil, ErrInvalidMessageType
	}
	b, err := json.Marshal(m.Data)
	if err != nil {
		return nil, err
	}
	var d ShardChunkData
	err = json.Unmarshal(b, &d)
	return &d, err
}

// Error types
var (
	ErrInvalidMessageType = errors.New("invalid message type")
)
