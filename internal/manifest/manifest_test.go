package manifest

import (
	"crypto/ed25519"
	"crypto/rand"
	"math/big"
	mrand "math/rand"
	"path/filepath"
	"testing"
	"time"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func mustShardHash(t *testing.T) [32]byte {
	t.Helper()
	var h [32]byte
	if _, err := rand.Read(h[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return h
}

func newSignedFile(t *testing.T, priv ed25519.PrivateKey, name string, shards [][32]byte) FileMeta {
	t.Helper()
	f := FileMeta{
		Filename:     name,
		Size:         42,
		DataShards:   4,
		ParityShards: 2,
		BlockSize:    1024,
		Shards:       shards,
		WrappedKey:   []byte("dummy"),
	}
	f.Sign(priv)
	return f
}

func TestFileMeta_SignVerify(t *testing.T) {
	_, priv := mustKey(t)
	f := newSignedFile(t, priv, "doc.txt", [][32]byte{mustShardHash(t)})
	if err := f.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	f.Filename = "tampered"
	if err := f.Verify(); err == nil {
		t.Fatal("expected verification failure after tampering")
	}
}

func TestManifest_AddFile_RejectsBadSignature(t *testing.T) {
	_, priv := mustKey(t)
	f := newSignedFile(t, priv, "doc.txt", nil)
	f.Signature[0] ^= 0xff
	m := New()
	if err := m.AddFile(f); err == nil {
		t.Fatal("expected AddFile to reject bad signature")
	}
}

func TestManifest_ShardReplicas_GrowOnly(t *testing.T) {
	m := New()
	h := mustShardHash(t)
	now := time.Now()
	m.MarkShardStored(h, "peer-a", now)
	m.MarkShardStored(h, "peer-b", now.Add(1*time.Second))
	m.MarkShardStored(h, "peer-a", now.Add(2*time.Second)) // duplicate is fine
	got := m.Replicas(h)
	if len(got) != 2 {
		t.Fatalf("Replicas count = %d, want 2", len(got))
	}
}

func TestManifest_TombstoneClearsReplicas(t *testing.T) {
	m := New()
	h := mustShardHash(t)
	now := time.Now()
	m.MarkShardStored(h, "peer-a", now)
	m.MarkShardStored(h, "peer-b", now)
	m.MarkShardDeleted(h, now.Add(1*time.Second))
	if got := m.Replicas(h); got != nil {
		t.Errorf("expected no replicas after tombstone, got %v", got)
	}
}

func TestManifest_FilesOwnedBy(t *testing.T) {
	owner1pub, owner1 := mustKey(t)
	_, owner2 := mustKey(t)
	m := New()
	if err := m.AddFile(newSignedFile(t, owner1, "a.txt", nil)); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := m.AddFile(newSignedFile(t, owner1, "b.txt", nil)); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := m.AddFile(newSignedFile(t, owner2, "c.txt", nil)); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	got := m.FilesOwnedBy(owner1pub)
	if len(got) != 2 {
		t.Fatalf("FilesOwnedBy returned %d files, want 2", len(got))
	}
	if got[0].Filename != "a.txt" || got[1].Filename != "b.txt" {
		t.Errorf("unexpected file order: %+v", got)
	}
}

// CRDT property tests --------------------------------------------------------

// generateRandomContracts builds a set of operations representing a
// realistic mix of file creates, store acks, and tombstones.
func generateRandomContracts(t *testing.T, seed int64) (apply func(*Manifest), expectedReplicas map[[32]byte]map[string]bool, expectedTombstoneFiles map[string]bool) {
	t.Helper()
	r := mrand.New(mrand.NewSource(seed))

	// Owners
	const numOwners = 3
	owners := make([]ed25519.PrivateKey, numOwners)
	ownerPubs := make([]ed25519.PublicKey, numOwners)
	for i := range owners {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		owners[i] = priv
		ownerPubs[i] = pub
	}

	type op any
	type addFileOp struct{ f FileMeta }
	type storeAckOp struct {
		hash [32]byte
		peer string
		when time.Time
	}
	type deleteFileOp struct {
		id   string
		when time.Time
	}
	type deleteShardOp struct {
		hash [32]byte
		when time.Time
	}

	expectedReplicas = map[[32]byte]map[string]bool{}
	expectedTombstoneFiles = map[string]bool{}
	tombstonedShards := map[[32]byte]bool{}

	var ops []op

	// Create some files
	const numFiles = 5
	files := make([]FileMeta, numFiles)
	for i := range files {
		ownerIdx := r.Intn(numOwners)
		shardCount := 2 + r.Intn(3)
		shards := make([][32]byte, shardCount)
		for j := range shards {
			shards[j] = mustShardHash(t)
		}
		f := newSignedFile(t, owners[ownerIdx], filenameFor(i), shards)
		files[i] = f
		ops = append(ops, addFileOp{f})
	}

	// Store acks across random peers
	const numAcks = 30
	for i := 0; i < numAcks; i++ {
		f := files[r.Intn(numFiles)]
		if len(f.Shards) == 0 {
			continue
		}
		shard := f.Shards[r.Intn(len(f.Shards))]
		peer := peerName(r.Intn(8))
		when := time.Now().Add(time.Duration(r.Intn(1000)) * time.Millisecond)
		ops = append(ops, storeAckOp{shard, peer, when})
		if expectedReplicas[shard] == nil {
			expectedReplicas[shard] = map[string]bool{}
		}
		expectedReplicas[shard][peer] = true
	}

	// Tombstone a couple of shards
	for i := 0; i < 3; i++ {
		f := files[r.Intn(numFiles)]
		if len(f.Shards) == 0 {
			continue
		}
		shard := f.Shards[r.Intn(len(f.Shards))]
		when := time.Now().Add(time.Hour) // make these always "win"
		ops = append(ops, deleteShardOp{shard, when})
		tombstonedShards[shard] = true
	}

	// Tombstone a file
	for i := 0; i < 2; i++ {
		f := files[r.Intn(numFiles)]
		when := time.Now().Add(time.Hour)
		ops = append(ops, deleteFileOp{f.ID(), when})
		expectedTombstoneFiles[f.ID()] = true
	}

	// After tombstones, expected replicas for tombstoned shards is empty.
	for h := range tombstonedShards {
		delete(expectedReplicas, h)
	}

	apply = func(m *Manifest) {
		// Shuffle a copy
		shuffled := make([]op, len(ops))
		copy(shuffled, ops)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, o := range shuffled {
			switch v := o.(type) {
			case addFileOp:
				_ = m.AddFile(v.f)
			case storeAckOp:
				m.MarkShardStored(v.hash, v.peer, v.when)
			case deleteFileOp:
				m.MarkFileDeleted(v.id, v.when)
			case deleteShardOp:
				m.MarkShardDeleted(v.hash, v.when)
			}
		}
	}
	return apply, expectedReplicas, expectedTombstoneFiles
}

func filenameFor(i int) string { return "file-" + bigInt(i) }
func peerName(i int) string    { return "peer-" + bigInt(i) }
func bigInt(i int) string      { return new(big.Int).SetInt64(int64(i)).String() }

func TestManifest_Merge_CommutativeAndIdempotent(t *testing.T) {
	apply, expectedReplicas, expectedTombstones := generateRandomContracts(t, 42)

	// Apply the ops in many different orders and confirm convergence.
	const trials = 5
	var snapshots []*Manifest
	for i := 0; i < trials; i++ {
		m := New()
		apply(m)
		snapshots = append(snapshots, m)
	}

	for i := 1; i < len(snapshots); i++ {
		assertManifestsEquivalent(t, snapshots[0], snapshots[i])
	}

	// Verify against the model
	for h, peers := range expectedReplicas {
		got := snapshots[0].Replicas(h)
		if len(got) != len(peers) {
			t.Errorf("shard %x: replica count = %d, want %d", h[:4], len(got), len(peers))
			continue
		}
		gotSet := map[string]bool{}
		for _, p := range got {
			gotSet[p] = true
		}
		for p := range peers {
			if !gotSet[p] {
				t.Errorf("shard %x: missing expected peer %s", h[:4], p)
			}
		}
	}
	for id := range expectedTombstones {
		f, ok := snapshots[0].File(id)
		if !ok || !f.Tombstone {
			t.Errorf("file %s: expected tombstone", id)
		}
	}

	// Idempotency: merging a manifest with itself does not change it.
	a := snapshots[0]
	a.Merge(snapshots[0].Snapshot())
	assertManifestsEquivalent(t, a, snapshots[1])
}

func TestManifest_Merge_Associative(t *testing.T) {
	apply, _, _ := generateRandomContracts(t, 7)

	a := New()
	b := New()
	c := New()
	apply(a)
	apply(b)
	apply(c)

	// (a ∪ b) ∪ c
	left := New()
	left.Merge(a.Snapshot())
	left.Merge(b.Snapshot())
	left.Merge(c.Snapshot())

	// a ∪ (b ∪ c)
	bc := New()
	bc.Merge(b.Snapshot())
	bc.Merge(c.Snapshot())
	right := New()
	right.Merge(a.Snapshot())
	right.Merge(bc)

	assertManifestsEquivalent(t, left, right)
}

func TestManifest_Merge_DivergentConverges(t *testing.T) {
	// Two independent "nodes" each apply the same set of ops in
	// different orders; merging their states yields identical final
	// manifests.
	apply, _, _ := generateRandomContracts(t, 19)

	nodeA := New()
	nodeB := New()
	apply(nodeA)
	apply(nodeB)

	merged1 := New()
	merged1.Merge(nodeA.Snapshot())
	merged1.Merge(nodeB.Snapshot())

	merged2 := New()
	merged2.Merge(nodeB.Snapshot())
	merged2.Merge(nodeA.Snapshot())

	assertManifestsEquivalent(t, merged1, merged2)
}

func assertManifestsEquivalent(t *testing.T, a, b *Manifest) {
	t.Helper()
	if len(a.files) != len(b.files) {
		t.Errorf("file count differs: %d vs %d", len(a.files), len(b.files))
	}
	for id, fa := range a.files {
		fb, ok := b.files[id]
		if !ok {
			t.Errorf("file %s present in a, missing in b", id)
			continue
		}
		if fa.Tombstone != fb.Tombstone {
			t.Errorf("file %s tombstone differs: %v vs %v", id, fa.Tombstone, fb.Tombstone)
		}
	}
	if len(a.shards) != len(b.shards) {
		t.Errorf("shard count differs: %d vs %d", len(a.shards), len(b.shards))
	}
	for h, sa := range a.shards {
		sb, ok := b.shards[h]
		if !ok {
			t.Errorf("shard %x present in a, missing in b", h[:4])
			continue
		}
		if sa.Tombstone != sb.Tombstone {
			t.Errorf("shard %x tombstone differs: %v vs %v", h[:4], sa.Tombstone, sb.Tombstone)
		}
		if len(sa.Replicas) != len(sb.Replicas) {
			t.Errorf("shard %x replica count differs: %d vs %d", h[:4], len(sa.Replicas), len(sb.Replicas))
		}
		for p := range sa.Replicas {
			if !sb.Replicas[p] {
				t.Errorf("shard %x peer %s in a not in b", h[:4], p)
			}
		}
	}
}

// Persistence tests ----------------------------------------------------------

func TestStore_PersistAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.db")

	owner, priv := mustKey(t)
	hash := mustShardHash(t)

	{
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		f := newSignedFile(t, priv, "saved.txt", [][32]byte{hash})
		if err := s.AddFile(f); err != nil {
			t.Fatalf("AddFile: %v", err)
		}
		if err := s.MarkShardStored(hash, "peer-x", time.Now()); err != nil {
			t.Fatalf("MarkShardStored: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()

	files := s.Manifest().FilesOwnedBy(owner)
	if len(files) != 1 || files[0].Filename != "saved.txt" {
		t.Fatalf("FilesOwnedBy after reopen = %+v, want one file", files)
	}
	got := s.Manifest().Replicas(hash)
	if len(got) != 1 || got[0] != "peer-x" {
		t.Fatalf("Replicas after reopen = %v, want [peer-x]", got)
	}
}
