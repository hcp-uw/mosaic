package shared

import (
	"os"
	"path/filepath"
)

// SocketPath is the Unix socket used for CLI↔daemon communication.
// Placed under HOME so two nodes with different HOME dirs can coexist on one machine.
var SocketPath = filepath.Join(os.Getenv("HOME"), ".mosaicd.sock")
