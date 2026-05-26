// Package shardstore persists shard data on the local filesystem. Each shard is
// one file inside a ".shards" directory under a configurable base directory
// (~/Mosaic by default); the address is base64url-encoded to form a
// filesystem-safe filename, so any address string can be used as a key.
package shardstore

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
)

// Store persists shards under Dir.
type Store struct {
	Dir string // the .shards directory
}

// DefaultBase returns the default Mosaic base directory, ~/Mosaic.
func DefaultBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Mosaic"), nil
}

// New returns a Store rooted at base/.shards, creating the directory.
func New(base string) (*Store, error) {
	dir := filepath.Join(base, ".shards")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// pathFor maps an address to its on-disk file path.
func (s *Store) pathFor(address string) string {
	name := base64.RawURLEncoding.EncodeToString([]byte(address))
	return filepath.Join(s.Dir, name)
}

// Put writes data for address, overwriting any existing shard at that address.
func (s *Store) Put(address string, data []byte) error {
	return os.WriteFile(s.pathFor(address), data, 0o600)
}

// Get returns the data stored for address. found is false (with a nil error)
// when no shard exists for that address.
func (s *Store) Get(address string) (data []byte, found bool, err error) {
	b, err := os.ReadFile(s.pathFor(address))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// ListAddresses returns all stored shard addresses.
func (s *Store) ListAddresses() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		address, err := base64.RawURLEncoding.DecodeString(e.Name())
		if err != nil {
			continue
		}
		out = append(out, string(address))
	}
	sort.Strings(out)
	return out, nil
}
