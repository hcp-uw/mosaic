package fileSystem

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
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
//   Those 32 bytes seed the P-256 key generation as a private scalar.
//
// Same login key on any machine → same 32-byte seed → same private key → same
// public key → can decrypt your own manifest entries and sign new ones.
//
// The derived key is cached to keyPath (0600) so the daemon does not need the
// login key in memory after startup. If keyPath already exists it is
// overwritten — this is intentional: re-logging-in refreshes the derived key.
func DeriveUserKeyFromLoginKey(loginKey string, keyPath string) (UserKeyPair, error) {
	if loginKey == "" {
		return UserKeyPair{}, fmt.Errorf("login key is empty: user must log in first (mos login account <username> <key>)")
	}

	// HKDF: extract + expand from the login key.
	// info string "mosaic-user-key" domain-separates this derivation from any
	// other keys we might derive from the same login key in future.
	seed, err := hkdf.Key(sha256.New, []byte(loginKey), nil, "mosaic-user-key", 32)
	if err != nil {
		return UserKeyPair{}, fmt.Errorf("HKDF derivation failed: %w", err)
	}

	// Use the 32-byte seed as the private scalar for P-256.
	// ecdsa.GenerateKey with a deterministic reader produces a deterministic key.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), newDeterministicReader(seed))
	if err != nil {
		return UserKeyPair{}, fmt.Errorf("could not derive keypair from seed: %w", err)
	}

	// Cache the derived key to disk so handlers can load it without the login key.
	pemData, err := marshalPrivateKeyPEM(priv)
	if err != nil {
		return UserKeyPair{}, err
	}
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		return UserKeyPair{}, fmt.Errorf("could not cache derived user key: %w", err)
	}

	return UserKeyPair{Private: priv, Public: &priv.PublicKey}, nil
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

// deterministicReader satisfies io.Reader by returning a fixed seed repeatedly.
// Used to make ecdsa.GenerateKey deterministic without exposing the seed
// directly as a private scalar (which would bypass P-256 validation checks).
type deterministicReader struct {
	data   []byte
	offset int
}

func newDeterministicReader(seed []byte) io.Reader {
	// Repeat the seed so ecdsa.GenerateKey always has enough bytes, regardless
	// of how many internal retries P-256 key generation needs.
	repeated := make([]byte, 512)
	for i := range repeated {
		repeated[i] = seed[i%len(seed)]
	}
	return &deterministicReader{data: repeated}
}

func (r *deterministicReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		// Fallback to crypto/rand if we somehow exhaust the buffer.
		return rand.Read(p)
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
