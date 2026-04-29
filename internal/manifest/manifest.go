// Package manifest is the CRDT-replicated index of files and shard
// locations across the cluster.
//
// Two entry types:
//
//   - FileMeta describes a file: owner, name, size, the ordered list of
//     shard hashes, Reed-Solomon params, and the wrapped per-file key.
//     Once created, FileMeta is immutable except for a monotonic
//     Tombstone flag (LWW on Updated for tiebreak).
//
//   - ShardReplica tracks which peers hold each shard hash. Replicas is
//     a grow-only set (G-Set) so concurrent merges always converge.
//     Tombstone is monotonic with the same LWW tiebreak.
//
// All mutations come in via signed contracts (FileCreated, StoreAck,
// FileDeleted). Contracts are verified before Apply, then merged
// idempotently into the manifest.
package manifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// FileMeta is the owner-asserted metadata for one stored file.
type FileMeta struct {
	Owner         ed25519.PublicKey `json:"owner"`
	Filename      string            `json:"filename"`
	Size          uint64            `json:"size"`           // plaintext size in bytes
	EncryptedSize uint64            `json:"encrypted_size"` // ciphertext size (nonce + plaintext + GCM tag)
	DataShards    uint32            `json:"data_shards"`
	ParityShards  uint32            `json:"parity_shards"`
	BlockSize     uint32            `json:"block_size"`
	Shards        [][32]byte        `json:"shards"`
	WrappedKey    []byte            `json:"wrapped_key"`
	Nonce         []byte            `json:"nonce,omitempty"` // reserved if encryption nonce is stored separately
	CreatedAt     time.Time         `json:"created_at"`
	Updated       time.Time         `json:"updated"`
	Tombstone     bool              `json:"tombstone"`
	Signature     []byte            `json:"signature"`
}

// FileID is the canonical identifier for a file: SHA-256 of (owner || filename).
// Two different owners can have files with the same filename — they get
// distinct FileIDs.
func FileID(owner ed25519.PublicKey, filename string) string {
	h := sha256.New()
	h.Write(owner)
	h.Write([]byte{0}) // separator to avoid filename||owner collisions
	h.Write([]byte(filename))
	return hex.EncodeToString(h.Sum(nil))
}

func (f *FileMeta) ID() string { return FileID(f.Owner, f.Filename) }

// signedBytes returns the canonical bytes the owner signs.
func (f *FileMeta) signedBytes() []byte {
	h := sha256.New()
	h.Write(f.Owner)
	h.Write([]byte(f.Filename))
	binary.Write(h, binary.BigEndian, f.Size)
	binary.Write(h, binary.BigEndian, f.EncryptedSize)
	binary.Write(h, binary.BigEndian, f.DataShards)
	binary.Write(h, binary.BigEndian, f.ParityShards)
	binary.Write(h, binary.BigEndian, f.BlockSize)
	for _, s := range f.Shards {
		h.Write(s[:])
	}
	h.Write(f.WrappedKey)
	h.Write(f.Nonce)
	binary.Write(h, binary.BigEndian, f.CreatedAt.UnixNano())
	binary.Write(h, binary.BigEndian, f.Tombstone)
	return h.Sum(nil)
}

// Sign fills the Signature field using the owner's private key.
// Updates the Updated timestamp to now.
func (f *FileMeta) Sign(priv ed25519.PrivateKey) {
	f.Owner = priv.Public().(ed25519.PublicKey)
	f.Updated = time.Now().UTC()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = f.Updated
	}
	f.Signature = ed25519.Sign(priv, f.signedBytes())
}

// Verify checks the owner's signature.
func (f *FileMeta) Verify() error {
	if len(f.Owner) != ed25519.PublicKeySize {
		return errors.New("invalid owner public key")
	}
	if !ed25519.Verify(f.Owner, f.signedBytes(), f.Signature) {
		return errors.New("file signature verification failed")
	}
	return nil
}

// ShardReplica is the per-shard CRDT entry.
type ShardReplica struct {
	Hash      [32]byte        `json:"hash"`
	Replicas  map[string]bool `json:"replicas"` // peer pubkey hex -> true
	Tombstone bool            `json:"tombstone"`
	Updated   time.Time       `json:"updated"`
}

// Manifest is the in-memory CRDT state.
type Manifest struct {
	mu     sync.RWMutex
	files  map[string]FileMeta      // FileID -> FileMeta
	shards map[[32]byte]ShardReplica
}

// New returns an empty manifest.
func New() *Manifest {
	return &Manifest{
		files:  map[string]FileMeta{},
		shards: map[[32]byte]ShardReplica{},
	}
}

// AddFile applies an owner's signed FileMeta. The signature must be valid.
func (m *Manifest) AddFile(meta FileMeta) error {
	if err := meta.Verify(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := meta.ID()
	cur, ok := m.files[id]
	if !ok {
		m.files[id] = meta
		return nil
	}
	m.files[id] = mergeFile(cur, meta)
	return nil
}

// MarkShardStored records that peerHex holds shardHash. Verification of
// the StoreAck signature happens in the caller — Apply (below) is the
// usual path.
func (m *Manifest) MarkShardStored(shardHash [32]byte, peerHex string, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.shards[shardHash]
	if !ok {
		cur = ShardReplica{
			Hash:     shardHash,
			Replicas: map[string]bool{},
			Updated:  when,
		}
	}
	if cur.Replicas == nil {
		cur.Replicas = map[string]bool{}
	}
	cur.Replicas[peerHex] = true
	if when.After(cur.Updated) {
		cur.Updated = when
	}
	m.shards[shardHash] = cur
}

// MarkFileDeleted tombstones the file. LWW on Updated.
func (m *Manifest) MarkFileDeleted(id string, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.files[id]
	if !ok {
		// Tombstone-without-create is recorded as an empty tombstoned entry
		// so a later concurrent add is dominated by the deletion.
		m.files[id] = FileMeta{Tombstone: true, Updated: when}
		return
	}
	if !cur.Tombstone || when.After(cur.Updated) {
		cur.Tombstone = true
		cur.Updated = laterOf(cur.Updated, when)
		m.files[id] = cur
	}
}

// MarkShardDeleted tombstones a shard. Owner-issued.
func (m *Manifest) MarkShardDeleted(shardHash [32]byte, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.shards[shardHash]
	if !ok {
		m.shards[shardHash] = ShardReplica{
			Hash:      shardHash,
			Replicas:  map[string]bool{},
			Tombstone: true,
			Updated:   when,
		}
		return
	}
	if !cur.Tombstone || when.After(cur.Updated) {
		cur.Tombstone = true
		cur.Updated = laterOf(cur.Updated, when)
		// Replicas stay in the G-Set so that MARK_STORED → MARK_DELETED
		// and MARK_DELETED → MARK_STORED converge to the same state. The
		// public Replicas() API filters them out when Tombstone is set.
		m.shards[shardHash] = cur
	}
}

// File returns a copy of a file's metadata.
func (m *Manifest) File(id string) (FileMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[id]
	return f, ok
}

// Shard returns a copy of a shard's replica entry.
func (m *Manifest) Shard(hash [32]byte) (ShardReplica, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.shards[hash]
	if !ok {
		return s, false
	}
	cp := s
	cp.Replicas = make(map[string]bool, len(s.Replicas))
	for k, v := range s.Replicas {
		cp.Replicas[k] = v
	}
	return cp, true
}

// Replicas returns the peer hex IDs claiming to hold the shard. Empty
// if the shard is tombstoned or unknown. Sorted for deterministic order.
func (m *Manifest) Replicas(hash [32]byte) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.shards[hash]
	if !ok || s.Tombstone {
		return nil
	}
	out := make([]string, 0, len(s.Replicas))
	for k, v := range s.Replicas {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// FilesOwnedBy returns all (non-tombstoned) files owned by the given key.
func (m *Manifest) FilesOwnedBy(pub ed25519.PublicKey) []FileMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []FileMeta
	for _, f := range m.files {
		if f.Tombstone {
			continue
		}
		if len(f.Owner) == len(pub) && string(f.Owner) == string(pub) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out
}

// AllFiles returns every non-tombstoned file in the manifest.
func (m *Manifest) AllFiles() []FileMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []FileMeta
	for _, f := range m.files {
		if !f.Tombstone {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out
}

// Snapshot returns a deep copy of the manifest's state.
func (m *Manifest) Snapshot() *Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := New()
	for k, v := range m.files {
		out.files[k] = v
	}
	for k, v := range m.shards {
		cp := v
		cp.Replicas = make(map[string]bool, len(v.Replicas))
		for rk, rv := range v.Replicas {
			cp.Replicas[rk] = rv
		}
		out.shards[k] = cp
	}
	return out
}

// Merge folds other into m. Commutative, associative, idempotent.
func (m *Manifest) Merge(other *Manifest) {
	other.mu.RLock()
	defer other.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, of := range other.files {
		if cur, ok := m.files[id]; ok {
			m.files[id] = mergeFile(cur, of)
		} else {
			m.files[id] = of
		}
	}
	for h, os := range other.shards {
		if cur, ok := m.shards[h]; ok {
			m.shards[h] = mergeShard(cur, os)
		} else {
			cp := os
			cp.Replicas = make(map[string]bool, len(os.Replicas))
			for k, v := range os.Replicas {
				cp.Replicas[k] = v
			}
			m.shards[h] = cp
		}
	}
}

func mergeFile(a, b FileMeta) FileMeta {
	// Pick the entry with stronger ownership info; prefer non-empty Owner.
	out := a
	if len(out.Owner) == 0 && len(b.Owner) > 0 {
		out = b
	}
	if len(b.Owner) > 0 && len(out.Owner) > 0 {
		// If both are real entries, prefer the one with non-zero CreatedAt.
		if out.CreatedAt.IsZero() && !b.CreatedAt.IsZero() {
			out = b
		}
	}
	// Tombstone wins under LWW with deterministic tiebreak.
	if a.Tombstone || b.Tombstone {
		out.Tombstone = true
		out.Updated = lwwLater(a.Updated, b.Updated)
	} else {
		out.Updated = lwwLater(a.Updated, b.Updated)
	}
	return out
}

func mergeShard(a, b ShardReplica) ShardReplica {
	out := ShardReplica{
		Hash:     a.Hash,
		Replicas: map[string]bool{},
	}
	if (out.Hash == [32]byte{}) {
		out.Hash = b.Hash
	}
	out.Tombstone = a.Tombstone || b.Tombstone
	out.Updated = lwwLater(a.Updated, b.Updated)
	for k, v := range a.Replicas {
		if v {
			out.Replicas[k] = true
		}
	}
	for k, v := range b.Replicas {
		if v {
			out.Replicas[k] = true
		}
	}
	return out
}

func lwwLater(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func laterOf(a, b time.Time) time.Time { return lwwLater(a, b) }
