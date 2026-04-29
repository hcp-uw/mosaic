package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Signed P2P payloads carry an ed25519 signature over the canonical
// concatenation of (timestamp || public_key || type-specific fields).
// Receivers MUST call Verify before trusting any field.
//
// These types are carried as the Data field of a Message envelope.

const (
	// PeerStoreRequest carries a SignedStoreRequest.
	PeerStoreRequest MessageType = "peer_store_request"
	// PeerStoreAck acknowledges a stored shard. Carries a SignedStoreAck.
	PeerStoreAck MessageType = "peer_store_ack"
	// PeerGetRequest fetches a shard by hash. Carries a GetRequest (unsigned).
	PeerGetRequest MessageType = "peer_get_request"
	// PeerGetResponse returns shard bytes (or empty if missing). Unsigned.
	PeerGetResponse MessageType = "peer_get_response"
	// PeerDeleteRequest signals shard deletion. Carries a SignedDeleteRequest.
	PeerDeleteRequest MessageType = "peer_delete_request"
	// PeerManifestUpdate gossips a batch of contracts to a peer.
	PeerManifestUpdate MessageType = "peer_manifest_update"
	// PeerGetMembers requests the current cluster membership from the leader.
	PeerGetMembers MessageType = "peer_get_members"
)

// MaxClockSkew is the +/- window beyond which signed messages are
// rejected as stale or future-dated.
const MaxClockSkew = 5 * time.Minute

var (
	ErrBadSignature   = errors.New("signature verification failed")
	ErrBadPublicKey   = errors.New("invalid ed25519 public key")
	ErrStaleTimestamp = errors.New("signed message timestamp outside clock-skew window")
	ErrHashMismatch   = errors.New("data hash does not match declared hash")
)

// SignedStoreRequest asks a peer to store a shard.
type SignedStoreRequest struct {
	Timestamp time.Time         `json:"timestamp"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Hash      []byte            `json:"hash"` // SHA-256 of Data
	Data      []byte            `json:"data"`
	Signature []byte            `json:"signature"`
}

// NewSignedStoreRequest signs a store request with the given private key.
// The signature covers timestamp || public_key || hash || data.
func NewSignedStoreRequest(priv ed25519.PrivateKey, data []byte) *SignedStoreRequest {
	sum := sha256.Sum256(data)
	req := &SignedStoreRequest{
		Timestamp: time.Now().UTC(),
		PublicKey: priv.Public().(ed25519.PublicKey),
		Hash:      sum[:],
		Data:      data,
	}
	req.Signature = ed25519.Sign(priv, req.signedBytes())
	return req
}

func (r *SignedStoreRequest) signedBytes() []byte {
	h := sha256.New()
	writeTimestamp(h, r.Timestamp)
	h.Write(r.PublicKey)
	h.Write(r.Hash)
	h.Write(r.Data)
	return h.Sum(nil)
}

// Verify checks the signature, hash, and timestamp window.
func (r *SignedStoreRequest) Verify() error {
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return ErrBadPublicKey
	}
	if !ed25519.Verify(r.PublicKey, r.signedBytes(), r.Signature) {
		return ErrBadSignature
	}
	if !timestampInWindow(r.Timestamp) {
		return ErrStaleTimestamp
	}
	sum := sha256.Sum256(r.Data)
	if !bytesEqual(sum[:], r.Hash) {
		return ErrHashMismatch
	}
	return nil
}

// SignedStoreAck is a signed receipt that the peer holds the shard.
// The owner records this in the manifest as proof a peer agreed to store it.
type SignedStoreAck struct {
	Timestamp time.Time         `json:"timestamp"`
	PublicKey ed25519.PublicKey `json:"public_key"` // signer = the storing peer
	Hash      []byte            `json:"hash"`
	Signature []byte            `json:"signature"`
}

// NewSignedStoreAck signs an acknowledgement.
func NewSignedStoreAck(priv ed25519.PrivateKey, hash []byte) *SignedStoreAck {
	ack := &SignedStoreAck{
		Timestamp: time.Now().UTC(),
		PublicKey: priv.Public().(ed25519.PublicKey),
		Hash:      append([]byte(nil), hash...),
	}
	ack.Signature = ed25519.Sign(priv, ack.signedBytes())
	return ack
}

func (a *SignedStoreAck) signedBytes() []byte {
	h := sha256.New()
	writeTimestamp(h, a.Timestamp)
	h.Write(a.PublicKey)
	h.Write(a.Hash)
	return h.Sum(nil)
}

func (a *SignedStoreAck) Verify() error {
	if len(a.PublicKey) != ed25519.PublicKeySize {
		return ErrBadPublicKey
	}
	if !ed25519.Verify(a.PublicKey, a.signedBytes(), a.Signature) {
		return ErrBadSignature
	}
	if !timestampInWindow(a.Timestamp) {
		return ErrStaleTimestamp
	}
	return nil
}

// SignedDeleteRequest asks peers to delete a shard. Only the original
// owner (the public key that uploaded) may issue these, but the on-wire
// envelope carries the signer key for verification.
type SignedDeleteRequest struct {
	Timestamp time.Time         `json:"timestamp"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Hash      []byte            `json:"hash"`
	Signature []byte            `json:"signature"`
}

func NewSignedDeleteRequest(priv ed25519.PrivateKey, hash []byte) *SignedDeleteRequest {
	req := &SignedDeleteRequest{
		Timestamp: time.Now().UTC(),
		PublicKey: priv.Public().(ed25519.PublicKey),
		Hash:      append([]byte(nil), hash...),
	}
	req.Signature = ed25519.Sign(priv, req.signedBytes())
	return req
}

func (r *SignedDeleteRequest) signedBytes() []byte {
	h := sha256.New()
	writeTimestamp(h, r.Timestamp)
	h.Write(r.PublicKey)
	h.Write(r.Hash)
	return h.Sum(nil)
}

func (r *SignedDeleteRequest) Verify() error {
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return ErrBadPublicKey
	}
	if !ed25519.Verify(r.PublicKey, r.signedBytes(), r.Signature) {
		return ErrBadSignature
	}
	if !timestampInWindow(r.Timestamp) {
		return ErrStaleTimestamp
	}
	return nil
}

// GetRequest is an unsigned shard fetch by hash.
type GetRequest struct {
	Hash []byte `json:"hash"`
}

// GetResponse returns shard bytes (or empty Data if not held).
type GetResponse struct {
	Hash []byte `json:"hash"`
	Data []byte `json:"data,omitempty"`
}

func writeTimestamp(h interface{ Write([]byte) (int, error) }, t time.Time) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(t.UTC().UnixNano()))
	if _, err := h.Write(buf[:]); err != nil {
		// hash.Hash.Write never errors per the stdlib contract; this is
		// here for any future writer that does.
		_ = fmt.Sprintf("%v", err)
	}
}

func timestampInWindow(t time.Time) bool {
	now := time.Now().UTC()
	delta := now.Sub(t)
	if delta < 0 {
		delta = -delta
	}
	return delta <= MaxClockSkew
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
