package helpers

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
)

// Session holds the local identity derived from the user's login key.
// No server involvement — everything is derived deterministically from the key.
type Session struct {
	PublicKey string `json:"publicKey"` // hex PKIX DER of ECDSA P-256 public key
}

// SaveSession writes the session to disk (0600).
func SaveSession(s Session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("could not marshal session: %w", err)
	}
	return os.WriteFile(shared.SessionPath(), data, 0600)
}

// LoadSession reads and returns the current session.
func LoadSession() (Session, error) {
	data, err := os.ReadFile(shared.SessionPath())
	if os.IsNotExist(err) {
		return Session{}, fmt.Errorf("not logged in — run 'mos login <key>'")
	}
	if err != nil {
		return Session{}, fmt.Errorf("could not read session: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("could not parse session: %w", err)
	}
	return s, nil
}

// IsLoggedIn returns true if a valid session exists.
func IsLoggedIn() bool {
	_, err := LoadSession()
	return err == nil
}

// GetToken is removed — no JWT in the keyless model.
// Kept as a stub returning "" so callers that haven't been updated yet don't break.
func GetToken() string { return "" }

// ClearSession removes the session file (called on logout).
func ClearSession() error {
	err := os.Remove(shared.SessionPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeriveAccountID returns a deterministic integer identity from the public key.
// Used wherever an int user ID is needed (e.g. manifest entries).
func DeriveAccountID(pubKeyHex string) int {
	h := sha256.Sum256([]byte(pubKeyHex))
	return int(binary.BigEndian.Uint32(h[:4]))
}
