// Package shardstore persists shard data on the local filesystem under
// ~/Mosaic/.shards. Each shard is one file; the address is base64url-encoded to
// form a filesystem-safe filename, so any address string can be used as a key.
package shardstore

import (
	"encoding/base64"
	"os"
	"path/filepath"
)

// Dir returns the shard storage directory (~/Mosaic/.shards), creating it if
// needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Mosaic", ".shards")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// pathFor maps an address to its on-disk file path within dir.
func pathFor(dir, address string) string {
	name := base64.RawURLEncoding.EncodeToString([]byte(address))
	return filepath.Join(dir, name)
}

// Store writes data for address, overwriting any existing shard at that address.
func Store(address string, data []byte) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return os.WriteFile(pathFor(dir, address), data, 0o600)
}

// Retrieve returns the data stored for address. found is false (with a nil
// error) when no shard exists for that address.
func Retrieve(address string) (data []byte, found bool, err error) {
	dir, err := Dir()
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(pathFor(dir, address))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
