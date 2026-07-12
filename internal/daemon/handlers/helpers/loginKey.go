package helpers

import (
	"fmt"
	"os"
	"strings"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

// SaveLoginKey persists the raw login key string to disk with 0600 permissions.
// Called by the login handler so subsequent daemon operations can derive the
// user keypair from it without re-prompting for the key.
func SaveLoginKey(key string) error {
	return os.WriteFile(shared.LoginKeyPath(), []byte(strings.TrimSpace(key)), 0600)
}

// LoadLoginKey reads the persisted login key, or returns ("", nil) if the user
// has not yet logged in. Callers that require a key should check for empty string.
func LoadLoginKey() (string, error) {
	data, err := os.ReadFile(shared.LoginKeyPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read login key: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ClearLoginKey removes the persisted login key (called on logout).
func ClearLoginKey() error {
	err := os.Remove(shared.LoginKeyPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
