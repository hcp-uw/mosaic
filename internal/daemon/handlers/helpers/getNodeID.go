package helpers

// GetNodeID returns 1. Without an auth server there is no multi-device node
// numbering — each installation is simply node 1.
func GetNodeID() int { return 1 }
