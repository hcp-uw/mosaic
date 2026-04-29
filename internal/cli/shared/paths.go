package shared

import "os"

// DefaultSocketPath is the fallback location for the daemon's Unix socket.
// Override with the MOSAIC_SOCKET environment variable to run multiple
// daemons on one machine (e.g. MOSAIC_SOCKET=/tmp/mosaicd-b.sock).
const DefaultSocketPath = "/tmp/mosaicd.sock"

// SocketPath returns the configured socket path, honoring $MOSAIC_SOCKET.
func SocketPath() string {
	if s := os.Getenv("MOSAIC_SOCKET"); s != "" {
		return s
	}
	return DefaultSocketPath
}
