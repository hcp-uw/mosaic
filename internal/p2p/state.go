package p2p

// ClientState represents the current state of the client
type ClientState int

const (
	StateDisconnected ClientState = iota
	StateConnecting
	StateWaiting
	StatePaired
	StateConnectedToPeer
	StateLeader
)

// String returns string representation of ClientState
func (s ClientState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StateWaiting:
		return "Waiting"
	case StatePaired:
		return "Paired"
	case StateConnectedToPeer:
		return "ConnectedToPeer"
	case StateLeader:
		return "Leader"
	default:
		return "Unknown"
	}
}
