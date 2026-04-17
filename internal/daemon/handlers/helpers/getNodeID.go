package helpers

// GetNodeID returns the authenticated user's node number (1, 2, 3...) from
// the local session. Returns 0 if the user is not logged in.
func GetNodeID() int {
	s, err := LoadSession()
	if err != nil {
		return 0
	}
	return s.NodeNumber
}
