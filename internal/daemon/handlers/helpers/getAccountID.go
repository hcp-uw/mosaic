package helpers

// GetAccountID returns a deterministic integer derived from the user's public key.
// Returns 0 if not logged in.
func GetAccountID() int {
	s, err := LoadSession()
	if err != nil {
		return 0
	}
	return DeriveAccountID(s.PublicKey)
}
