package p2p

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
)

// Handshake identity binding.
//
// The X25519 exchange alone gives an *encrypted* channel but proves nothing about
// WHO is on the other end — a man-in-the-middle could substitute its own ephemeral
// keys and sit in the middle. To close that, each side signs its handshake with
// its long-term account ECDSA key. The signature covers the ephemeral public key
// (binding this session's Diffie-Hellman to the account) plus the account key and
// node ID (so those fields can't be swapped). An attacker can't produce a valid
// signature for a victim's account without the victim's private key, so it can't
// impersonate an account or splice itself between two honest peers.
//
// Note this authenticates *which account* the peer is, not *whether it's allowed*
// — Mosaic is permissionless, so anyone with an account may connect. The value is
// unforgeable identity, established peer-to-peer with no server involvement.

// handshakeTranscript is the byte string signed/verified for a handshake. All
// three inputs are fields of the HandshakeInit message, so the transcript is
// self-contained and the verifier recomputes it from the received message.
func handshakeTranscript(accountPubDER, ephemeralPub []byte, nodeID string) []byte {
	h := sha256.New()
	h.Write([]byte("mosaic-handshake-v1\x00"))
	h.Write(accountPubDER)
	h.Write(ephemeralPub)
	h.Write([]byte(nodeID))
	return h.Sum(nil)
}

// signHandshake produces the ECDSA (ASN.1) signature over the transcript using
// the account private key. Returns nil if priv is nil (not logged in).
func signHandshake(priv *ecdsa.PrivateKey, accountPubDER, ephemeralPub []byte, nodeID string) ([]byte, error) {
	if priv == nil {
		return nil, nil
	}
	return ecdsa.SignASN1(rand.Reader, priv, handshakeTranscript(accountPubDER, ephemeralPub, nodeID))
}

// verifyHandshake checks that signature is a valid account-key signature over the
// transcript for the given fields. Returns true only on a cryptographically valid
// signature — a missing/short/forged signature or a malformed account key fails.
func verifyHandshake(accountPubDER, ephemeralPub []byte, nodeID string, signature []byte) bool {
	if len(accountPubDER) == 0 || len(signature) == 0 {
		return false
	}
	pub, err := x509.ParsePKIXPublicKey(accountPubDER)
	if err != nil {
		return false
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	return ecdsa.VerifyASN1(ecPub, handshakeTranscript(accountPubDER, ephemeralPub, nodeID), signature)
}
