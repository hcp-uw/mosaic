package helpers

// GetUsername returns the authenticated user's username from the local session.
// Returns an empty string if the user is not logged in.
func GetUsername() string {
	s, err := LoadSession()
	if err != nil {
		return ""
	}
	return s.Username
}
