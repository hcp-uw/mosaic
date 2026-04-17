package helpers

// GetAccountID returns the authenticated user's account ID from the local
// session. Returns 0 if the user is not logged in.
func GetAccountID() int {
	s, err := LoadSession()
	if err != nil {
		return 0
	}
	return s.AccountID
}
