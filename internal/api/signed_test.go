package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return priv
}

func TestSignedStoreRequest_RoundTrip(t *testing.T) {
	priv := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello world"))
	if err := req.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// JSON round-trip preserves all fields.
	enc, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got SignedStoreRequest
	if err := json.Unmarshal(enc, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("Verify after JSON round-trip: %v", err)
	}
}

func TestSignedStoreRequest_TamperedDataRejected(t *testing.T) {
	priv := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello"))
	req.Data[0] = 'H'
	if err := req.Verify(); err == nil {
		t.Fatal("expected verification failure after tampering with Data")
	}
}

func TestSignedStoreRequest_TamperedHashRejected(t *testing.T) {
	priv := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello"))
	req.Hash[0] ^= 0xff
	if err := req.Verify(); err == nil {
		t.Fatal("expected verification failure after tampering with Hash")
	}
}

func TestSignedStoreRequest_WrongKeyRejected(t *testing.T) {
	priv := mustKey(t)
	other := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello"))
	req.PublicKey = other.Public().(ed25519.PublicKey)
	if err := req.Verify(); err == nil {
		t.Fatal("expected verification failure when public key does not match signer")
	}
}

func TestSignedStoreRequest_StaleTimestamp(t *testing.T) {
	priv := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello"))
	req.Timestamp = time.Now().Add(-2 * MaxClockSkew)
	// Re-sign with the stale timestamp so signature verifies but window check fails.
	req.Signature = ed25519.Sign(priv, req.signedBytes())
	if err := req.Verify(); err != ErrStaleTimestamp {
		t.Fatalf("expected ErrStaleTimestamp, got %v", err)
	}
}

func TestSignedStoreAck_RoundTrip(t *testing.T) {
	priv := mustKey(t)
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	ack := NewSignedStoreAck(priv, hash)
	if err := ack.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	enc, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got SignedStoreAck
	if err := json.Unmarshal(enc, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("Verify after JSON round-trip: %v", err)
	}
}

func TestSignedDeleteRequest_RoundTrip(t *testing.T) {
	priv := mustKey(t)
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	req := NewSignedDeleteRequest(priv, hash)
	if err := req.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	enc, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got SignedDeleteRequest
	if err := json.Unmarshal(enc, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("Verify after JSON round-trip: %v", err)
	}
}

func TestSignedDeleteRequest_TamperedHashRejected(t *testing.T) {
	priv := mustKey(t)
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	req := NewSignedDeleteRequest(priv, hash)
	req.Hash[0] ^= 0xff
	if err := req.Verify(); err == nil {
		t.Fatal("expected verification failure after tampering with Hash")
	}
}

func TestVerify_RejectsShortPublicKey(t *testing.T) {
	priv := mustKey(t)
	req := NewSignedStoreRequest(priv, []byte("hello"))
	req.PublicKey = req.PublicKey[:16]
	if err := req.Verify(); err != ErrBadPublicKey {
		t.Fatalf("expected ErrBadPublicKey, got %v", err)
	}
}
