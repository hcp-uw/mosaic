// Package identity manages the node's long-lived ed25519 keypair.
//
// The keypair is generated on first start and persisted to disk so that
// the node has a stable cryptographic identity across restarts. The public
// key, encoded as hex, also serves as the node's logical ID inside the
// cluster (peers reference each other by hex-encoded public keys).
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Identity is a node's persistent ed25519 keypair.
type Identity struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// LoadOrCreate returns the identity stored at path, creating a new one if
// the file does not exist. The file holds the 32-byte ed25519 seed and is
// written with 0600 permissions.
func LoadOrCreate(path string) (*Identity, error) {
	if path == "" {
		return nil, errors.New("identity path is empty")
	}

	if data, err := os.ReadFile(path); err == nil {
		if len(data) != ed25519.SeedSize {
			return nil, fmt.Errorf("identity file %s has size %d, want %d", path, len(data), ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(data)
		return &Identity{
			Private: priv,
			Public:  priv.Public().(ed25519.PublicKey),
		}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read identity: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir identity dir: %w", err)
	}

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("generate seed: %w", err)
	}
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		return nil, fmt.Errorf("write identity: %w", err)
	}

	priv := ed25519.NewKeyFromSeed(seed)
	return &Identity{
		Private: priv,
		Public:  priv.Public().(ed25519.PublicKey),
	}, nil
}

// PublicKeyHex returns the hex encoding of the public key. This doubles as
// the node's stable cluster ID.
func (id *Identity) PublicKeyHex() string {
	return hex.EncodeToString(id.Public)
}

// PublicKeyFromHex decodes a hex-encoded ed25519 public key.
func PublicKeyFromHex(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode hex pubkey: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("pubkey has size %d, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}
