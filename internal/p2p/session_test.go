package p2p

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"net"
	"testing"

	"github.com/hcp-uw/mosaic/internal/api"
)

// signedInit builds a HandshakeInit signed with the given account key, as a real
// peer would. completeHandshake now rejects unsigned/invalid handshakes, so tests
// must produce a valid signature.
func signedInit(t *testing.T, account *ecdsa.PrivateKey, senderID string, ephPub []byte, nodeID string) *api.Message {
	t.Helper()
	pubDER, err := x509.MarshalPKIXPublicKey(&account.PublicKey)
	if err != nil {
		t.Fatalf("marshal account pub: %v", err)
	}
	sig, err := signHandshake(account, pubDER, ephPub, nodeID)
	if err != nil {
		t.Fatalf("sign handshake: %v", err)
	}
	return api.NewHandshakeInitMessage(senderID, ephPub, 0, nodeID, pubDER, sig)
}

func testAccountKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen account key: %v", err)
	}
	return k
}

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
	msgFromB := signedInit(t, testAccountKey(t), "peerB", ephB.PublicKey().Bytes(), "")
	clientA.completeHandshake(msgFromB, peerBOnA)

	// Build Client B: has stored ephB private key; receives A's public key.
	peerAOnB := &PeerInfo{
		ID:              "peerA",
		EphemeralPrivKey: ephB.Bytes(),
	}
	clientB := &Client{
		peers: map[string]*PeerInfo{"peerA": peerAOnB},
	}
	msgFromA := signedInit(t, testAccountKey(t), "peerA", ephA.PublicKey().Bytes(), "")
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
	clientA.completeHandshake(signedInit(t, testAccountKey(t), "peerB", ephB.PublicKey().Bytes(), "node-B"), peerBOnA)

	peerAOnB := &PeerInfo{ID: "peerA", EphemeralPrivKey: ephB.Bytes()}
	clientB := &Client{peers: map[string]*PeerInfo{"peerA": peerAOnB}}
	clientB.completeHandshake(signedInit(t, testAccountKey(t), "peerA", ephA.PublicKey().Bytes(), "node-A"), peerAOnB)

	// The peer's stable STUN node ID must propagate across the handshake — this is
	// what lets holder tracking map a session's transport to a stable identity.
	if peerBOnA.StunNodeID != "node-B" {
		t.Errorf("peer B's StunNodeID: got %q, want %q", peerBOnA.StunNodeID, "node-B")
	}
	if peerAOnB.StunNodeID != "node-A" {
		t.Errorf("peer A's StunNodeID: got %q, want %q", peerAOnB.StunNodeID, "node-A")
	}

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

// TestPlaintextAllowed verifies the control-message authentication policy: only
// the handshake bootstrap and pre-session liveness pings may arrive unencrypted;
// everything else must be authenticated (arrive inside the session envelope).
func TestPlaintextAllowed(t *testing.T) {
	c := &Client{peers: map[string]*PeerInfo{
		"established": {ID: "established", HandshakeDone: true},
		"connecting":  {ID: "connecting", HandshakeDone: false},
	}}

	// HandshakeInit is always allowed plaintext (the session key can't exist yet).
	if !c.plaintextAllowed(&api.Message{Type: api.HandshakeInit}) {
		t.Error("HandshakeInit must be allowed plaintext")
	}

	// A ping claiming to be an established peer must be authenticated.
	if c.plaintextAllowed(&api.Message{Type: api.PeerPing, Sign: api.NewSignature("established")}) {
		t.Error("plaintext ping from an established peer must be rejected")
	}
	// Pings during setup (peer not yet established, or unknown) are allowed.
	if !c.plaintextAllowed(&api.Message{Type: api.PeerPong, Sign: api.NewSignature("connecting")}) {
		t.Error("plaintext pong during setup must be allowed")
	}
	if !c.plaintextAllowed(&api.Message{Type: api.PeerPing, Sign: api.NewSignature("unknown")}) {
		t.Error("plaintext ping from an unknown peer must be allowed (no session yet)")
	}

	// Dangerous control messages are never allowed plaintext.
	for _, mt := range []api.MessageType{
		api.ShardRequest, api.NodeLeave, api.ShardDelete, api.ManifestSync,
		api.ShardStreamDone, api.ShardStreamAck, api.IdentityResponse, api.PeerTextMessage,
	} {
		if c.plaintextAllowed(&api.Message{Type: mt, Sign: api.NewSignature("established")}) {
			t.Errorf("%s must never be allowed plaintext", mt)
		}
	}
}

// TestCompleteHandshake_RejectsUnsigned verifies the core of the identity-binding
// work: a handshake with no account signature establishes no session. Without
// this, a man-in-the-middle could simply omit the signature.
func TestCompleteHandshake_RejectsUnsigned(t *testing.T) {
	ephA, _ := ecdh.X25519().GenerateKey(rand.Reader)
	ephB, _ := ecdh.X25519().GenerateKey(rand.Reader)

	peerBOnA := &PeerInfo{ID: "peerB", EphemeralPrivKey: ephA.Bytes()}
	clientA := &Client{peers: map[string]*PeerInfo{"peerB": peerBOnA}}

	unsigned := api.NewHandshakeInitMessage("peerB", ephB.PublicKey().Bytes(), 0, "", nil, nil)
	clientA.completeHandshake(unsigned, peerBOnA)
	if peerBOnA.HandshakeDone {
		t.Fatal("unsigned handshake must be rejected — no session may be established")
	}
}

// TestCompleteHandshake_RejectsForgedSignature simulates the MITM: an attacker
// presents its own ephemeral key but a signature computed over a DIFFERENT key.
// Because verification recomputes the transcript from the ephemeral key actually
// in the message, the mismatch is caught and the handshake is rejected.
func TestCompleteHandshake_RejectsForgedSignature(t *testing.T) {
	ephA, _ := ecdh.X25519().GenerateKey(rand.Reader)
	ephB, _ := ecdh.X25519().GenerateKey(rand.Reader)

	peerBOnA := &PeerInfo{ID: "peerB", EphemeralPrivKey: ephA.Bytes()}
	clientA := &Client{peers: map[string]*PeerInfo{"peerB": peerBOnA}}

	attacker := testAccountKey(t)
	pubDER, _ := x509.MarshalPKIXPublicKey(&attacker.PublicKey)
	wrongEph, _ := ecdh.X25519().GenerateKey(rand.Reader)
	sig, _ := signHandshake(attacker, pubDER, wrongEph.PublicKey().Bytes(), "") // over the wrong key
	forged := api.NewHandshakeInitMessage("peerB", ephB.PublicKey().Bytes(), 0, "", pubDER, sig)

	clientA.completeHandshake(forged, peerBOnA)
	if peerBOnA.HandshakeDone {
		t.Fatal("handshake with a signature over the wrong ephemeral key must be rejected")
	}
}
