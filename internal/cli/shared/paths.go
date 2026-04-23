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

// SessionPath returns ~/.mosaic-session — the persisted session file.
func SessionPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-session")
}
