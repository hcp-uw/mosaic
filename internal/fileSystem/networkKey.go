package fileSystem

import (
	"crypto/rand"
	"fmt"
	"os"
)

// LoadOrCreateNetworkKey returns the 32-byte AES-256 master key used to
// encrypt the network manifest. If the key file does not exist it generates
// a new random key, persists it with 0600 permissions, and returns it.
// keyPath should be filepath.Join(os.Getenv("HOME"), ".mosaic-network.key").
func LoadOrCreateNetworkKey(keyPath string) ([32]byte, error) {
	var key [32]byte

	data, err := os.ReadFile(keyPath)
	if err == nil {
		if len(data) != 32 {
			return key, fmt.Errorf("network key file at %s is corrupt (expected 32 bytes, got %d)", keyPath, len(data))
		}
		copy(key[:], data)
		return key, nil
	}

	if !os.IsNotExist(err) {
		return key, fmt.Errorf("could not read network key: %w", err)
	}

	// Generate a new random key.
	if _, err := rand.Read(key[:]); err != nil {
		return key, fmt.Errorf("could not generate network key: %w", err)
	}

	if err := os.WriteFile(keyPath, key[:], 0600); err != nil {
		return key, fmt.Errorf("could not save network key: %w", err)
	}

	return key, nil
}
