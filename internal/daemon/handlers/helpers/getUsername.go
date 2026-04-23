package helpers

// GetUsername returns a short display identifier derived from the public key.
// Returns "" if not logged in.
func GetUsername() string {
	s, err := LoadSession()
	if err != nil {
		return ""
	}
	if len(s.PublicKey) >= 8 {
		return s.PublicKey[:8]
	}
	return s.PublicKey
}
