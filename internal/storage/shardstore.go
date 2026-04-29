// Package storage provides on-disk, hash-indexed storage for shards.
//
// Shards are addressed by their SHA-256 content hash and laid out in a
// two-level fanout (<hex[0:2]>/<hex>.dat) so directory listings stay
// fast even with millions of shards. The store is concurrency-safe for
// concurrent readers and writers of distinct hashes; concurrent writes
// of the same hash are serialized by the underlying filesystem rename.
package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Hash is the content hash of a shard. SHA-256 is 32 bytes.
type Hash [32]byte

func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }

// ErrNotFound is returned by Get and Delete when the shard is absent.
var ErrNotFound = errors.New("shard not found")

// ShardStore stores shards on the local filesystem under a single root.
type ShardStore struct {
	dir       string
	usedBytes atomic.Uint64 // cached on init; kept in sync by Put/Delete
}

// New opens (and creates if necessary) a shard store rooted at dir. The
// initial used-bytes total is computed by walking the existing files.
func New(dir string) (*ShardStore, error) {
	if dir == "" {
		return nil, errors.New("shard store dir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir shard root: %w", err)
	}
	s := &ShardStore{dir: dir}
	used, err := walkUsedBytes(dir)
	if err != nil {
		return nil, err
	}
	s.usedBytes.Store(used)
	return s, nil
}

// Dir returns the root directory of the store.
func (s *ShardStore) Dir() string { return s.dir }

// path returns the full filesystem path for a hash, ensuring the
// containing fanout directory exists.
func (s *ShardStore) path(h Hash) (string, error) {
	hex := h.Hex()
	subdir := filepath.Join(s.dir, hex[:2])
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir shard subdir: %w", err)
	}
	return filepath.Join(subdir, hex+".dat"), nil
}

// Has reports whether the shard is present.
func (s *ShardStore) Has(h Hash) bool {
	hex := h.Hex()
	_, err := os.Stat(filepath.Join(s.dir, hex[:2], hex+".dat"))
	return err == nil
}

// Put writes the shard atomically (write to temp file, then rename).
// If the shard already exists with the same size, the call is a no-op.
func (s *ShardStore) Put(h Hash, data []byte) error {
	final, err := s.path(h)
	if err != nil {
		return err
	}
	if info, err := os.Stat(final); err == nil && info.Size() == int64(len(data)) {
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(final), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}

	// Atomic rename — concurrent writers of the same hash converge on
	// the same final bytes, so whichever wins is fine.
	prevSize := int64(0)
	if info, err := os.Stat(final); err == nil {
		prevSize = info.Size()
	}
	if err := os.Rename(tmpPath, final); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	delta := int64(len(data)) - prevSize
	s.addUsed(delta)
	return nil
}

// Get returns the shard bytes, or ErrNotFound.
func (s *ShardStore) Get(h Hash) ([]byte, error) {
	hex := h.Hex()
	data, err := os.ReadFile(filepath.Join(s.dir, hex[:2], hex+".dat"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read shard: %w", err)
	}
	return data, nil
}

// Delete removes a shard. Missing shards return ErrNotFound.
func (s *ShardStore) Delete(h Hash) error {
	hex := h.Hex()
	full := filepath.Join(s.dir, hex[:2], hex+".dat")
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if err := os.Remove(full); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	s.addUsed(-info.Size())
	return nil
}

// UsedBytes returns the cached total bytes consumed by stored shards.
// The value is maintained by Put/Delete; it is recomputed from disk
// only when the store is opened.
func (s *ShardStore) UsedBytes() uint64 {
	return s.usedBytes.Load()
}

// List returns the hex-encoded hashes of every shard in the store, in
// arbitrary order. Used by emptyStorage and accounting helpers.
func (s *ShardStore) List() ([]Hash, error) {
	var out []Hash
	err := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if filepath.Ext(name) != ".dat" {
			return nil
		}
		hexStr := name[:len(name)-len(".dat")]
		raw, err := hex.DecodeString(hexStr)
		if err != nil || len(raw) != 32 {
			return nil
		}
		var h Hash
		copy(h[:], raw)
		out = append(out, h)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ShardStore) addUsed(delta int64) {
	if delta == 0 {
		return
	}
	if delta > 0 {
		s.usedBytes.Add(uint64(delta))
		return
	}
	// Subtract — guard against underflow if the cache drifted.
	abs := uint64(-delta)
	for {
		cur := s.usedBytes.Load()
		var next uint64
		if abs > cur {
			next = 0
		} else {
			next = cur - abs
		}
		if s.usedBytes.CompareAndSwap(cur, next) {
			return
		}
	}
}

func walkUsedBytes(dir string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".dat" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
