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

	DefaultServerIP = "178.128.151.84"

	// DefaultSTUNServer is the production STUN server address.
	// Change this one constant if the droplet IP or port ever changes.
	DefaultSTUNServer = DefaultServerIP + ":3478"

	// DefaultTURNServer is the TURN (UDP relay) address — same droplet, port 3479.
	// Intentionally disabled: the UDP TURN tier had unresolved stability issues, and
	// a half-open TURN session (relay-server dial succeeds but the peer never connects
	// through it) marks the peer ViaTURN and blocks fall-through to the TCP relay.
	// The TCP relay below is the reliable fallback. Re-enable only once TURN is
	// verified stable end-to-end: DefaultTURNServer = DefaultServerIP + ":3479".
	DefaultTURNServer = ""

	// TURNUsername and TURNPassword are the shared credentials for the relay.
	TURNUsername = "mosaic"
	TURNPassword = "mosaic-turn"

	// DefaultTCPRelayServer is the TCP relay address — same droplet, port 443 (TLS).
	// This is the fallback when direct UDP hole-punching fails (symmetric NAT) or UDP
	// is blocked entirely (e.g. university WiFi). Port 443 is used because restrictive
	// firewalls that drop UDP and arbitrary high ports almost universally allow
	// outbound TCP 443 (HTTPS). Must match mosaic-stun's -relay-port (default 443).
	DefaultTCPRelayServer = DefaultServerIP + ":443"
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

// StunNodeIDPath returns ~/.mosaic-stun-id — a stable, per-machine random ID sent
// to the STUN server so it can restore this node's queue position after a restart.
func StunNodeIDPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mosaic-stun-id")
}
