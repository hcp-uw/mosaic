package p2p

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"net"
	"testing"

	"github.com/hcp-uw/mosaic/internal/api"
)

// newPeerWithKey builds a PeerInfo with an explicit session key for testing.
func newPeerWithKey(key [32]byte) *PeerInfo {
	return &PeerInfo{
		SessionKey:    key,
		HandshakeDone: true,
	}
}

// TestSealOpen_Roundtrip verifies that sealForPeer / openFromPeer are inverses.
func TestSealOpen_Roundtrip(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	peer := newPeerWithKey(key)

	plaintext := []byte("hello, mosaic session layer")
	ct, err := peer.sealForPeer(plaintext)
	if err != nil {
		t.Fatalf("sealForPeer: %v", err)
	}

	got, err := peer.openFromPeer(ct)
	if err != nil {
		t.Fatalf("openFromPeer: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("roundtrip mismatch: got %q, want %q", got, plaintext)
	}
}

// TestSealOpen_WrongKey verifies that decryption with a different key fails.
func TestSealOpen_WrongKey(t *testing.T) {
	var key [32]byte
	rand.Read(key[:]) //nolint:errcheck

	peer := newPeerWithKey(key)
	ct, err := peer.sealForPeer([]byte("secret"))
	if err != nil {
		t.Fatalf("sealForPeer: %v", err)
	}

	var otherKey [32]byte
	otherKey[0] = 0xFF // differ from key
	otherPeer := newPeerWithKey(otherKey)
	_, err = otherPeer.openFromPeer(ct)
	if err == nil {
		t.Fatal("expected decryption error with wrong key, got nil")
	}
}

// TestSealOpen_MagicByte verifies the 0x02 magic is the first byte of sealed frames.
func TestSealOpen_MagicByte(t *testing.T) {
	var key [32]byte
	rand.Read(key[:]) //nolint:errcheck
	peer := newPeerWithKey(key)

	ct, err := peer.sealForPeer([]byte("test"))
	if err != nil {
		t.Fatalf("sealForPeer: %v", err)
	}
	if len(ct) == 0 || ct[0] != sessionEncryptedMagic {
		t.Errorf("first byte: got 0x%02x, want 0x%02x", ct[0], sessionEncryptedMagic)
	}
}

// TestSealOpen_TooShort verifies that openFromPeer rejects truncated frames.
func TestSealOpen_TooShort(t *testing.T) {
	var key [32]byte
	peer := newPeerWithKey(key)
	_, err := peer.openFromPeer([]byte{0x02, 0x00}) // way too short
	if err == nil {
		t.Fatal("expected error for too-short frame, got nil")
	}
}

// TestSealOpen_Tampering verifies that bit-flipping the ciphertext is detected.
func TestSealOpen_Tampering(t *testing.T) {
	var key [32]byte
	rand.Read(key[:]) //nolint:errcheck
	peer := newPeerWithKey(key)

	ct, _ := peer.sealForPeer([]byte("tamper me"))
	ct[len(ct)-1] ^= 0xFF // flip last byte of GCM tag

	_, err := peer.openFromPeer(ct)
	if err == nil {
		t.Fatal("expected authentication error after tampering, got nil")
	}
}

// TestGetPeerByAddr verifies that getPeerByAddr returns the correct peer.
func TestGetPeerByAddr(t *testing.T) {
	addr1, _ := net.ResolveUDPAddr("udp", "10.0.0.1:4000")
	addr2, _ := net.ResolveUDPAddr("udp", "10.0.0.2:4000")

	c := &Client{
		peers: map[string]*PeerInfo{
			"peer1": {ID: "peer1", Address: addr1},
			"peer2": {ID: "peer2", Address: addr2},
		},
	}

	got := c.getPeerByAddr(addr1)
	if got == nil || got.ID != "peer1" {
		t.Errorf("getPeerByAddr(%s): got %v, want peer1", addr1, got)
	}

	got = c.getPeerByAddr(addr2)
	if got == nil || got.ID != "peer2" {
		t.Errorf("getPeerByAddr(%s): got %v, want peer2", addr2, got)
	}
}

// TestGetPeerByAddr_Missing verifies nil return for an unknown address.
func TestGetPeerByAddr_Missing(t *testing.T) {
	c := &Client{peers: map[string]*PeerInfo{}}
	addr, _ := net.ResolveUDPAddr("udp", "1.2.3.4:9999")
	if got := c.getPeerByAddr(addr); got != nil {
		t.Errorf("expected nil for unknown addr, got %v", got)
	}
}

// TestGetPeerByAddr_Nil verifies nil return when addr is nil.
func TestGetPeerByAddr_Nil(t *testing.T) {
	c := &Client{peers: map[string]*PeerInfo{}}
	if got := c.getPeerByAddr(nil); got != nil {
		t.Errorf("expected nil for nil addr, got %v", got)
	}
}

// TestCompleteHandshake_SymmetricKey verifies that two peers independently
// derive the same AES-256-GCM session key from their X25519 key exchange.
//
// This mirrors the real handshake:
//   A generates ephA, sends pubA  →  B calls completeHandshake with pubA
//   B generates ephB, sends pubB  →  A calls completeHandshake with pubB
//   Both compute X25519(myPriv, theirPub) → HKDF → sessionKey
func TestCompleteHandshake_SymmetricKey(t *testing.T) {
	// Generate two ephemeral keypairs.
	ephA, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ephA: %v", err)
	}
	ephB, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ephB: %v", err)
	}

	// Build Client A: has stored ephA private key; receives B's public key.
	peerBOnA := &PeerInfo{
		ID:              "peerB",
		EphemeralPrivKey: ephA.Bytes(),
	}
	clientA := &Client{
		peers: map[string]*PeerInfo{"peerB": peerBOnA},
	}
	msgFromB := api.NewHandshakeInitMessage("peerB", ephB.PublicKey().Bytes())
	clientA.completeHandshake(msgFromB, peerBOnA)

	// Build Client B: has stored ephB private key; receives A's public key.
	peerAOnB := &PeerInfo{
		ID:              "peerA",
		EphemeralPrivKey: ephB.Bytes(),
	}
	clientB := &Client{
		peers: map[string]*PeerInfo{"peerA": peerAOnB},
	}
	msgFromA := api.NewHandshakeInitMessage("peerA", ephA.PublicKey().Bytes())
	clientB.completeHandshake(msgFromA, peerAOnB)

	// Both sides must have completed the handshake.
	if !peerBOnA.HandshakeDone {
		t.Fatal("A: HandshakeDone not set")
	}
	if !peerAOnB.HandshakeDone {
		t.Fatal("B: HandshakeDone not set")
	}

	// Session keys must be identical.
	if peerBOnA.SessionKey != peerAOnB.SessionKey {
		t.Errorf("session key mismatch:\n A computed: %x\n B computed: %x",
			peerBOnA.SessionKey, peerAOnB.SessionKey)
	}

	// Ephemeral private keys must be wiped.
	if peerBOnA.EphemeralPrivKey != nil {
		t.Error("A: EphemeralPrivKey not cleared after handshake")
	}
	if peerAOnB.EphemeralPrivKey != nil {
		t.Error("B: EphemeralPrivKey not cleared after handshake")
	}
}

// TestCompleteHandshake_CrossEncrypt verifies end-to-end: after the handshake
// both peers can seal/open each other's messages.
func TestCompleteHandshake_CrossEncrypt(t *testing.T) {
	ephA, _ := ecdh.X25519().GenerateKey(rand.Reader)
	ephB, _ := ecdh.X25519().GenerateKey(rand.Reader)

	peerBOnA := &PeerInfo{ID: "peerB", EphemeralPrivKey: ephA.Bytes()}
	clientA := &Client{peers: map[string]*PeerInfo{"peerB": peerBOnA}}
	clientA.completeHandshake(api.NewHandshakeInitMessage("peerB", ephB.PublicKey().Bytes()), peerBOnA)

	peerAOnB := &PeerInfo{ID: "peerA", EphemeralPrivKey: ephB.Bytes()}
	clientB := &Client{peers: map[string]*PeerInfo{"peerA": peerAOnB}}
	clientB.completeHandshake(api.NewHandshakeInitMessage("peerA", ephA.PublicKey().Bytes()), peerAOnB)

	// A encrypts a message; B decrypts it.
	msg := []byte("encrypted across the handshake")
	ct, err := peerBOnA.sealForPeer(msg)
	if err != nil {
		t.Fatalf("sealForPeer: %v", err)
	}
	got, err := peerAOnB.openFromPeer(ct)
	if err != nil {
		t.Fatalf("openFromPeer: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Errorf("cross-decrypt mismatch: got %q, want %q", got, msg)
	}
}
