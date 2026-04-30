package shared

import (
	"os"
	"path/filepath"
)

const (
	// SocketPath is the Unix socket used for CLI↔daemon IPC.
	SocketPath = "/tmp/mosaicd.sock"

	// DaemonPIDFile and DaemonLogFile are the daemon's runtime files on Unix.
	DaemonPIDFile = "/tmp/mosaicd.pid"
	DaemonLogFile = "/tmp/mosaicd.log"

	// DefaultSTUNServer is the production STUN server address.
	// Change this one constant if the droplet IP or port ever changes.
	DefaultSTUNServer = "178.128.151.84:3478"

	// DefaultTURNServer is the TURN relay address — same droplet, port 3479.
	// Used as fallback when STUN hole-punching fails (e.g. restrictive NAT/firewall).
	DefaultTURNServer = "178.128.151.84:3479"

	// TURNUsername and TURNPassword are the shared credentials for the relay.
	TURNUsername = "mosaic"
	TURNPassword = "mosaic-turn"
)

// MosaicDir returns ~/Mosaic — the user's file storage directory.
func MosaicDir() string {
	return filepath.Join(os.Getenv("HOME"), "Mosaic")
}

// LoginKeyPath returns ~/.mosaic-login.key — the raw login key on disk.
func LoginKeyPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-login.key")
}

// UserKeyPath returns ~/.mosaic-user.key — the derived ECDSA private key.
func UserKeyPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-user.key")
}

// NetworkKeyPath returns ~/.mosaic-network.key — the AES key that encrypts
// the network manifest at rest on disk.
func NetworkKeyPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-network.key")
}

// ShardKeyPath returns ~/.mosaic-shard.key — the 32-byte AES-256 key used to
// encrypt and decrypt shard data. Derived from the login key at login time and
// cached here so the raw login key never needs to be stored on disk.
func ShardKeyPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-shard.key")
}

// SessionPath returns ~/.mosaic-session — the persisted session file.
func SessionPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-session")
}
