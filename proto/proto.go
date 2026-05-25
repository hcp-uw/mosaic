// Package proto defines the RPC layer that clients speak on top of the relay's
// plaintext, line-oriented transport.
//
// The relay forwards every line verbatim (prefixed with the sender's address)
// to all other clients. On top of that, clients exchange one JSON-encoded
// Message per line: a request names a method and carries its parameters, and
// each client that receives a request runs the method and sends back a response
// carrying the result. Lines that are not valid Messages (e.g. plain chat text)
// are left for the caller to handle however it likes.
package proto

import "encoding/json"

// Message types.
const (
	TypeRequest  = "request"
	TypeResponse = "response"
)

// Methods.
const (
	MethodStoreShard    = "store_shard"
	MethodRetrieveShard = "retrieve_shard"
)

// Message is a single RPC envelope, serialized as one newline-free JSON line.
//
// A request sets Type=request, a unique ID, Method, and Params. A response
// echoes the same ID and Method, sets Type=response, and carries Result.
type Message struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// StoreShardParams are the arguments to store_shard.
type StoreShardParams struct {
	Address string `json:"address"`
	Data    []byte `json:"data"` // JSON-encoded as base64
}

// StoreShardResult is the reply from store_shard.
type StoreShardResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// RetrieveShardParams are the arguments to retrieve_shard.
type RetrieveShardParams struct {
	Address string `json:"address"`
}

// RetrieveShardResult is the reply from retrieve_shard. Found is false when the
// responding client holds no shard for the requested address.
type RetrieveShardResult struct {
	Found bool   `json:"found"`
	Data  []byte `json:"data,omitempty"` // JSON-encoded as base64
	Error string `json:"error,omitempty"`
}

// Encode marshals m into a single JSON line (no trailing newline).
func Encode(m *Message) (string, error) {
	b, err := json.Marshal(m)
	return string(b), err
}

// Decode parses a line into a Message. It returns ok=false when the line is not
// one of our JSON envelopes (for example, a plaintext chat message), so callers
// can fall back to treating the line as raw text.
func Decode(line string) (m *Message, ok bool) {
	var msg Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil, false
	}
	if msg.Type != TypeRequest && msg.Type != TypeResponse {
		return nil, false
	}
	return &msg, true
}
