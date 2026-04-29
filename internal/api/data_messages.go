package api

import (
	"encoding/json"
	"time"
)

// Constructors for the Message envelope wrapping each data-op payload.

func NewPeerStoreRequestMessage(senderID string, req *SignedStoreRequest) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerStoreRequest,
		Timestamp: time.Now(),
		Data:      req,
	}
}

func NewPeerStoreAckMessage(senderID string, ack *SignedStoreAck) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerStoreAck,
		Timestamp: time.Now(),
		Data:      ack,
	}
}

func NewPeerGetRequestMessage(senderID string, req *GetRequest) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerGetRequest,
		Timestamp: time.Now(),
		Data:      req,
	}
}

func NewPeerGetResponseMessage(senderID string, resp *GetResponse) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerGetResponse,
		Timestamp: time.Now(),
		Data:      resp,
	}
}

func NewPeerDeleteRequestMessage(senderID string, req *SignedDeleteRequest) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerDeleteRequest,
		Timestamp: time.Now(),
		Data:      req,
	}
}

// PeerManifestUpdateData carries one or more contracts (raw signed bytes)
// that the sender wants the receiver to apply.
type PeerManifestUpdateData struct {
	FileMetas    []json.RawMessage `json:"file_metas,omitempty"`
	StoreAcks    []json.RawMessage `json:"store_acks,omitempty"`
	DeleteFiles  []json.RawMessage `json:"delete_files,omitempty"`
	DeleteShards []json.RawMessage `json:"delete_shards,omitempty"`
}

func NewPeerManifestUpdateMessage(senderID string, data PeerManifestUpdateData) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerManifestUpdate,
		Timestamp: time.Now(),
		Data:      data,
	}
}

func NewPeerGetMembersMessage(senderID string) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerGetMembers,
		Timestamp: time.Now(),
	}
}

// PeerChunkedFrame carries one fragment of a larger message that did not
// fit in a single UDP datagram. Receivers reassemble by TransferID and
// dispatch the assembled bytes through the normal message path.
const PeerChunkedFrame MessageType = "peer_chunked_frame"

type ChunkedFrameData struct {
	TransferID uint64 `json:"id"`
	Index      uint32 `json:"i"`
	Total      uint32 `json:"n"`
	Payload    []byte `json:"p"`
}

func NewPeerChunkedFrameMessage(senderID string, transferID uint64, index, total uint32, payload []byte) *Message {
	return &Message{
		Sign:      Signature{PubKey: senderID},
		Type:      PeerChunkedFrame,
		Timestamp: time.Now(),
		Data: ChunkedFrameData{
			TransferID: transferID,
			Index:      index,
			Total:      total,
			Payload:    payload,
		},
	}
}

func (m *Message) GetChunkedFrame() (*ChunkedFrameData, error) {
	if m.Type != PeerChunkedFrame {
		return nil, ErrInvalidMessageType
	}
	var data ChunkedFrameData
	if err := remarshal(m.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// Extractors --------------------------------------------------------------

func (m *Message) GetStoreRequest() (*SignedStoreRequest, error) {
	if m.Type != PeerStoreRequest {
		return nil, ErrInvalidMessageType
	}
	var req SignedStoreRequest
	if err := remarshal(m.Data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (m *Message) GetStoreAck() (*SignedStoreAck, error) {
	if m.Type != PeerStoreAck {
		return nil, ErrInvalidMessageType
	}
	var ack SignedStoreAck
	if err := remarshal(m.Data, &ack); err != nil {
		return nil, err
	}
	return &ack, nil
}

func (m *Message) GetGetRequest() (*GetRequest, error) {
	if m.Type != PeerGetRequest {
		return nil, ErrInvalidMessageType
	}
	var req GetRequest
	if err := remarshal(m.Data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (m *Message) GetGetResponse() (*GetResponse, error) {
	if m.Type != PeerGetResponse {
		return nil, ErrInvalidMessageType
	}
	var resp GetResponse
	if err := remarshal(m.Data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *Message) GetDeleteRequest() (*SignedDeleteRequest, error) {
	if m.Type != PeerDeleteRequest {
		return nil, ErrInvalidMessageType
	}
	var req SignedDeleteRequest
	if err := remarshal(m.Data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (m *Message) GetManifestUpdate() (*PeerManifestUpdateData, error) {
	if m.Type != PeerManifestUpdate {
		return nil, ErrInvalidMessageType
	}
	var data PeerManifestUpdateData
	if err := remarshal(m.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func remarshal(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
