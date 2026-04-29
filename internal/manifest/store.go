package manifest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketFiles  = []byte("files")
	bucketShards = []byte("shards")
)

// Store is a BoltDB-backed persistent manifest. All mutations are
// applied to both the in-memory Manifest and the on-disk database.
type Store struct {
	db  *bolt.DB
	mem *Manifest

	mu sync.Mutex // serializes write transactions
}

// Open opens (creating if needed) the manifest at path and loads any
// existing entries into memory.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open boltdb: %w", err)
	}
	s := &Store{db: db, mem: New()}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketFiles, bucketShards} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) load() error {
	return s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketFiles); b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var f FileMeta
				if err := json.Unmarshal(v, &f); err != nil {
					return fmt.Errorf("decode file %s: %w", k, err)
				}
				s.mem.files[string(k)] = f
				return nil
			}); err != nil {
				return err
			}
		}
		if b := tx.Bucket(bucketShards); b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var sh ShardReplica
				if err := json.Unmarshal(v, &sh); err != nil {
					return fmt.Errorf("decode shard %s: %w", k, err)
				}
				if sh.Replicas == nil {
					sh.Replicas = map[string]bool{}
				}
				var key [32]byte
				if len(k) != 32 {
					return fmt.Errorf("shard key has length %d, want 32", len(k))
				}
				copy(key[:], k)
				s.mem.shards[key] = sh
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Close flushes and closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Manifest returns the in-memory view (read access only — use the
// Store's mutating methods for changes).
func (s *Store) Manifest() *Manifest { return s.mem }

// AddFile applies a signed FileMeta and persists it.
func (s *Store) AddFile(meta FileMeta) error {
	if err := s.mem.AddFile(meta); err != nil {
		return err
	}
	stored, _ := s.mem.File(meta.ID())
	return s.persistFile(meta.ID(), stored)
}

// MarkShardStored persists a new replica claim.
func (s *Store) MarkShardStored(hash [32]byte, peerHex string, when time.Time) error {
	s.mem.MarkShardStored(hash, peerHex, when)
	cur, _ := s.mem.Shard(hash)
	return s.persistShard(hash, cur)
}

// MarkFileDeleted tombstones a file by ID.
func (s *Store) MarkFileDeleted(id string, when time.Time) error {
	s.mem.MarkFileDeleted(id, when)
	cur, ok := s.mem.File(id)
	if !ok {
		return nil
	}
	return s.persistFile(id, cur)
}

// MarkShardDeleted tombstones a shard by hash.
func (s *Store) MarkShardDeleted(hash [32]byte, when time.Time) error {
	s.mem.MarkShardDeleted(hash, when)
	cur, ok := s.mem.Shard(hash)
	if !ok {
		return nil
	}
	return s.persistShard(hash, cur)
}

func (s *Store) persistFile(id string, meta FileMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode file: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketFiles).Put([]byte(id), enc)
	})
}

func (s *Store) persistShard(hash [32]byte, sh ShardReplica) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := json.Marshal(sh)
	if err != nil {
		return fmt.Errorf("encode shard: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketShards).Put(hash[:], enc)
	})
}

// hexKey is provided for debugging / inspection.
func hexKey(b []byte) string { return hex.EncodeToString(b) }
