package helpers

import (
	"crypto/sha256"
	"encoding/hex"
)

// GetUsername returns the 8-char SHA-256 fingerprint of the user's public key.
// Returns "" if not logged in.
func GetUsername() string {
	s, err := LoadSession()
	if err != nil {
		return ""
	}
	raw, err := hex.DecodeString(s.PublicKey)
	if err != nil || len(raw) == 0 {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])[:8]
}
