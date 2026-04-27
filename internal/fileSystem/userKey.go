package fileSystem

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
)

// UserKeyPair holds an ECDSA P-256 keypair for signing manifest entries.
// The private key is derived deterministically from the user's login key,
// so the same keypair is produced on any machine where the user logs in.
type UserKeyPair struct {
	Private *ecdsa.PrivateKey
	Public  *ecdsa.PublicKey
}

// DeriveUserKeyFromLoginKey derives a deterministic ECDSA P-256 keypair from
// the user's login key string using HKDF-SHA256.
//
// Derivation:
//   HKDF(hash=SHA-256, ikm=loginKey, salt=nil, info="mosaic-user-key") → 32 bytes
//   Those 32 bytes are used directly as the P-256 private scalar via ecdh.P256().
//
// Same login key on any machine → same 32-byte seed → same private key → same
// public key → can decrypt your own manifest entries and sign new ones.
//
// Note: ecdsa.GenerateKey in Go 1.22+ ignores the caller-supplied io.Reader and
// uses its own internal CSPRNG, making a deterministic reader approach non-functional.
// We use ecdh.P256().NewPrivateKey(seed) instead, which accepts raw scalar bytes
// and is guaranteed to be deterministic.
func DeriveUserKeyFromLoginKey(loginKey string, keyPath string) (UserKeyPair, error) {
	if loginKey == "" {
		return UserKeyPair{}, fmt.Errorf("login key is empty: user must log in first (mos login <key>)")
	}

	seed, err := hkdf.Key(sha256.New, []byte(loginKey), nil, "mosaic-user-key", 32)
	if err != nil {
		return UserKeyPair{}, fmt.Errorf("HKDF derivation failed: %w", err)
	}

	// NewPrivateKey accepts the raw 32-byte scalar and validates it is in [1, N-1].
	// HKDF output is uniformly random so the probability of an out-of-range value
	// is ~2^-128 — effectively impossible.
	ecdhKey, err := ecdh.P256().NewPrivateKey(seed)
	if err != nil {
		return UserKeyPair{}, fmt.Errorf("derived key scalar is invalid: %w", err)
	}

	priv, err := ecdhKeyToECDSA(ecdhKey)
	if err != nil {
		return UserKeyPair{}, err
	}

	pemData, err := marshalPrivateKeyPEM(priv)
	if err != nil {
		return UserKeyPair{}, err
	}
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		return UserKeyPair{}, fmt.Errorf("could not cache derived user key: %w", err)
	}

	return UserKeyPair{Private: priv, Public: &priv.PublicKey}, nil
}

// ecdhKeyToECDSA converts an *ecdh.PrivateKey to *ecdsa.PrivateKey by
// extracting the raw scalar and public key coordinates.
func ecdhKeyToECDSA(key *ecdh.PrivateKey) (*ecdsa.PrivateKey, error) {
	pub := key.PublicKey().Bytes() // uncompressed: 04 || X (32B) || Y (32B)
	if len(pub) != 65 || pub[0] != 0x04 {
		return nil, fmt.Errorf("unexpected ECDH public key encoding (len=%d)", len(pub))
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(pub[1:33]),
			Y:     new(big.Int).SetBytes(pub[33:65]),
		},
		D: new(big.Int).SetBytes(key.Bytes()),
	}, nil
}

// LoadOrCreateUserKey loads the cached ECDSA keypair from keyPath.
// If no cached key exists yet (user has not logged in on this machine since
// the key was introduced), it returns an error directing the user to log in.
// The login handler calls DeriveUserKeyFromLoginKey which writes the cache.
//
// keyPath should be filepath.Join(os.Getenv("HOME"), ".mosaic-user.key").
func LoadOrCreateUserKey(keyPath string) (UserKeyPair, error) {
	data, err := os.ReadFile(keyPath)
	if err == nil {
		return parsePrivateKeyPEM(data)
	}

	if os.IsNotExist(err) {
		return UserKeyPair{}, fmt.Errorf(
			"no user key found — run 'mos login account <username> <key>' to derive your keypair",
		)
	}

	return UserKeyPair{}, fmt.Errorf("could not read user key: %w", err)
}

// PublicKeyBytes serializes the ECDSA public key to PKIX DER bytes
// suitable for embedding in UserNetworkEntry.PublicKey.
func PublicKeyBytes(pub *ecdsa.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(pub)
}

// ParsePublicKeyBytes deserializes a public key produced by PublicKeyBytes.
func ParsePublicKeyBytes(data []byte) (*ecdsa.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("could not parse public key: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not ECDSA")
	}
	return ecPub, nil
}

func marshalPrivateKeyPEM(priv *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("could not marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func parsePrivateKeyPEM(data []byte) (UserKeyPair, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return UserKeyPair{}, fmt.Errorf("user key file contains no PEM block")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return UserKeyPair{}, fmt.Errorf("could not parse user private key: %w", err)
	}
	return UserKeyPair{Private: priv, Public: &priv.PublicKey}, nil
}

