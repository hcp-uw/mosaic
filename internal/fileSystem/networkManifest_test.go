package fileSystem

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

// makeTestKeyPair generates a fresh P-256 keypair for testing.
func makeTestKeyPair(t *testing.T) UserKeyPair {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return UserKeyPair{Private: priv, Public: &priv.PublicKey}
}

func testFile(name, contentHash string) NetworkFileEntry {
	return NetworkFileEntry{Name: name, Size: 100, PrimaryNodeID: 1, ContentHash: contentHash, DateAdded: "01-01-2026"}
}

func emptyManifest() NetworkManifest {
	return NetworkManifest{Version: 3, Users: make(map[int]*UserState), ShardMap: make(map[string]*ShardLocations)}
}

// ──────────────────────────────────────────────────────────
// RecordFileAdd / GetUserFiles
// ──────────────────────────────────────────────────────────

func TestRecordFileAdd_CreatesUser(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()

	if err := RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp); err != nil {
		t.Fatalf("RecordFileAdd: %v", err)
	}

	if !UserExistsInNetwork(m, 1) {
		t.Fatal("user 1 should exist after RecordFileAdd")
	}
}

func TestRecordFileAdd_DecryptedByOwner(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	metaKey := MetaKeyFromKP(kp)
	files := GetUserFiles(m, 1, &metaKey)
	if len(files) != 1 || files[0].Name != "a.txt" {
		t.Fatalf("expected [a.txt], got %v", files)
	}
}

func TestRecordFileAdd_NonOwnerSeesOnlyHash(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	files := GetUserFiles(m, 1, nil)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].ContentHash != "hash-a" {
		t.Fatalf("expected contentHash=hash-a, got %q", files[0].ContentHash)
	}
	if files[0].Name != "" {
		t.Fatalf("non-owner should see empty Name, got %q", files[0].Name)
	}
}

// ──────────────────────────────────────────────────────────
// RecordFileRemove
// ──────────────────────────────────────────────────────────

func TestRecordFileRemove_Tombstone(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)
	RecordFileAdd(&m, 1, "alice", testFile("b.txt", "hash-b"), kp)

	if err := RecordFileRemove(&m, 1, "hash-a", kp); err != nil {
		t.Fatalf("RecordFileRemove: %v", err)
	}

	metaKey := MetaKeyFromKP(kp)
	files := GetUserFiles(m, 1, &metaKey)
	if len(files) != 1 || files[0].Name != "b.txt" {
		t.Fatalf("expected [b.txt], got %v", files)
	}
}

func TestRecordFileRemove_AllFiles(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)
	RecordFileRemove(&m, 1, "hash-a", kp)

	files := GetUserFiles(m, 1, nil)
	if len(files) != 0 {
		t.Fatalf("expected empty file set after removing all files, got %v", files)
	}
}

// ──────────────────────────────────────────────────────────
// RecordFileRename
// ──────────────────────────────────────────────────────────

func TestRecordFileRename_UpdatesName(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("old.txt", "hash-a"), kp)

	if err := RecordFileRename(&m, 1, "hash-a", "new.txt", kp); err != nil {
		t.Fatalf("RecordFileRename: %v", err)
	}

	metaKey := MetaKeyFromKP(kp)
	files := GetUserFiles(m, 1, &metaKey)
	if len(files) != 1 || files[0].Name != "new.txt" {
		t.Fatalf("expected [new.txt], got %v", files)
	}
}

func TestRecordFileRename_NonexistentErrors(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	err := RecordFileRename(&m, 1, "nonexistent", "new.txt", kp)
	if err == nil {
		t.Fatal("expected error renaming nonexistent file")
	}
}

// ──────────────────────────────────────────────────────────
// Signature verification
// ──────────────────────────────────────────────────────────

func TestVerifyRecord_TamperedContentHash(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	r := m.Users[1].Records["hash-a"]
	r.ContentHash = "evil-hash"

	pub := &kp.Private.PublicKey
	if verifyRecord(1, r, pub) {
		t.Fatal("tampered ContentHash should fail verification")
	}
}

func TestVerifyRecord_TamperedSeq(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	r := m.Users[1].Records["hash-a"]
	r.Seq = 999

	pub := &kp.Private.PublicKey
	if verifyRecord(1, r, pub) {
		t.Fatal("tampered Seq should fail verification")
	}
}

func TestVerifyRecord_TamperedSignature(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	r := m.Users[1].Records["hash-a"]
	r.Signature[0] ^= 0xFF // flip bits

	pub := &kp.Private.PublicKey
	if verifyRecord(1, r, pub) {
		t.Fatal("corrupted signature should fail verification")
	}
}

// ──────────────────────────────────────────────────────────
// MergeNetworkManifest (LWW)
// ──────────────────────────────────────────────────────────

func TestMerge_NewUserFromRemote(t *testing.T) {
	kp := makeTestKeyPair(t)
	local := emptyManifest()
	remote := emptyManifest()
	RecordFileAdd(&remote, 42, "bob", testFile("a.txt", "hash-a"), kp)

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true when a new user is merged in")
	}
	if !UserExistsInNetwork(merged, 42) {
		t.Fatal("user 42 should exist in merged manifest")
	}
}

func TestMerge_HigherSeqWins(t *testing.T) {
	kp := makeTestKeyPair(t)
	local := emptyManifest()
	remote := emptyManifest()

	// local: add a.txt (seq=1)
	RecordFileAdd(&local, 1, "alice", testFile("a.txt", "hash-a"), kp)
	// remote: also add a.txt then rename (seq=2)
	RecordFileAdd(&remote, 1, "alice", testFile("a.txt", "hash-a"), kp)
	RecordFileRename(&remote, 1, "hash-a", "renamed.txt", kp)

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true when remote has higher Seq")
	}
	metaKey := MetaKeyFromKP(kp)
	files := GetUserFiles(merged, 1, &metaKey)
	if len(files) != 1 || files[0].Name != "renamed.txt" {
		t.Fatalf("expected [renamed.txt], got %v", files)
	}
}

func TestMerge_LowerSeqIgnored(t *testing.T) {
	kp := makeTestKeyPair(t)
	local := emptyManifest()
	remote := emptyManifest()

	// local: add then rename (seq=2)
	RecordFileAdd(&local, 1, "alice", testFile("a.txt", "hash-a"), kp)
	RecordFileRename(&local, 1, "hash-a", "renamed.txt", kp)
	// remote: only add (seq=1)
	RecordFileAdd(&remote, 1, "alice", testFile("a.txt", "hash-a"), kp)

	merged, changed := MergeNetworkManifest(local, remote)
	if changed {
		t.Fatal("expected changed=false when remote has lower Seq")
	}
	metaKey := MetaKeyFromKP(kp)
	files := GetUserFiles(merged, 1, &metaKey)
	if len(files) != 1 || files[0].Name != "renamed.txt" {
		t.Fatalf("local state should be preserved; got %v", files)
	}
}

func TestMerge_InvalidRecordDropped(t *testing.T) {
	kp := makeTestKeyPair(t)
	remote := emptyManifest()
	RecordFileAdd(&remote, 1, "alice", testFile("a.txt", "hash-a"), kp)

	// Tamper with the record after signing.
	remote.Users[1].Records["hash-a"].ContentHash = "evil"

	local := emptyManifest()
	merged, changed := MergeNetworkManifest(local, remote)
	if changed {
		t.Fatal("expected changed=false — tampered record should be dropped")
	}
	_ = merged
}

func TestMerge_Idempotent(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	_, changed := MergeNetworkManifest(m, m)
	if changed {
		t.Fatal("expected changed=false when merging identical manifests")
	}
}

// ──────────────────────────────────────────────────────────
// AllNetworkFiles
// ──────────────────────────────────────────────────────────

func TestAllNetworkFiles_MultipleUsers(t *testing.T) {
	kp1 := makeTestKeyPair(t)
	kp2 := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp1)
	RecordFileAdd(&m, 2, "bob", testFile("b.txt", "hash-b"), kp2)
	RecordFileRemove(&m, 1, "hash-a", kp1)

	files := AllNetworkFiles(m)
	if len(files) != 1 {
		t.Fatalf("expected 1 non-deleted file, got %d", len(files))
	}
	if files[0].ContentHash != "hash-b" {
		t.Fatalf("expected hash-b, got %q", files[0].ContentHash)
	}
}

// ──────────────────────────────────────────────────────────
// FindFileByName
// ──────────────────────────────────────────────────────────

func TestFindFileByName_Found(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	metaKey := MetaKeyFromKP(kp)
	ch, ok := FindFileByName(m, 1, "a.txt", &metaKey)
	if !ok || ch != "hash-a" {
		t.Fatalf("expected (hash-a, true), got (%q, %v)", ch, ok)
	}
}

func TestFindFileByName_NotFound(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := emptyManifest()
	RecordFileAdd(&m, 1, "alice", testFile("a.txt", "hash-a"), kp)

	metaKey := MetaKeyFromKP(kp)
	_, ok := FindFileByName(m, 1, "missing.txt", &metaKey)
	if ok {
		t.Fatal("expected not found for missing file")
	}
}

// ──────────────────────────────────────────────────────────
// ShardMap — RecordShardHolder / GetShardHolders / merge
// ──────────────────────────────────────────────────────────

func TestRecordShardHolder_Basic(t *testing.T) {
	m := emptyManifest()
	changed := RecordShardHolder(&m, "abc123", 0, "node-A")
	if !changed {
		t.Fatal("expected changed=true on first record")
	}
	holders := GetShardHolders(m, "abc123", 0)
	if len(holders) != 1 || holders[0] != "node-A" {
		t.Fatalf("expected [node-A], got %v", holders)
	}
}

func TestRecordShardHolder_Idempotent(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "abc123", 0, "node-A")
	changed := RecordShardHolder(&m, "abc123", 0, "node-A")
	if changed {
		t.Fatal("expected changed=false when holder already recorded")
	}
	if len(GetShardHolders(m, "abc123", 0)) != 1 {
		t.Fatal("duplicate entry added despite idempotent contract")
	}
}

func TestRecordShardHolder_MultipleHolders(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "abc123", 0, "node-A")
	RecordShardHolder(&m, "abc123", 0, "node-B")
	RecordShardHolder(&m, "abc123", 1, "node-C")

	if len(GetShardHolders(m, "abc123", 0)) != 2 {
		t.Fatal("expected 2 holders for shard 0")
	}
	if len(GetShardHolders(m, "abc123", 1)) != 1 {
		t.Fatal("expected 1 holder for shard 1")
	}
}

func TestGetShardHolders_Unknown(t *testing.T) {
	m := emptyManifest()
	if GetShardHolders(m, "nope", 0) != nil {
		t.Fatal("expected nil for unknown file hash")
	}
	RecordShardHolder(&m, "abc123", 0, "node-A")
	if GetShardHolders(m, "abc123", 99) != nil {
		t.Fatal("expected nil for unknown shard index")
	}
}

func TestMerge_ShardMapUnion(t *testing.T) {
	local := emptyManifest()
	RecordShardHolder(&local, "abc123", 0, "node-A")

	remote := emptyManifest()
	RecordShardHolder(&remote, "abc123", 0, "node-B")
	RecordShardHolder(&remote, "abc123", 1, "node-C")

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true when remote has new holders")
	}
	if len(GetShardHolders(merged, "abc123", 0)) != 2 {
		t.Fatal("expected node-A and node-B for shard 0")
	}
	if len(GetShardHolders(merged, "abc123", 1)) != 1 {
		t.Fatal("expected node-C for shard 1")
	}
}

func TestMerge_ShardMapNewFile(t *testing.T) {
	local := emptyManifest()
	remote := emptyManifest()
	RecordShardHolder(&remote, "deadbeef", 0, "node-X")

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true for entirely new file in shard map")
	}
	if len(GetShardHolders(merged, "deadbeef", 0)) != 1 {
		t.Fatal("expected node-X as holder after merge")
	}
}

func TestMerge_ShardMapIdempotent(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "abc123", 0, "node-A")

	_, changed := MergeNetworkManifest(m, m)
	if changed {
		t.Fatal("expected changed=false when shard maps are identical")
	}
}

// TestReadNetworkManifest_V3ToV4Migration verifies that reading a v3 manifest
// drops the (unstable) ShardMap and upgrades to v4 while preserving the signed
// file records. This is the one-time clean-up for the holder-ID rework.
func TestReadNetworkManifest_V3ToV4Migration(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	// Build and persist a v3 manifest with a user record and stale ShardMap data.
	v3 := NetworkManifest{
		Version: 3,
		Users: map[int]*UserState{
			7: {UserID: 7, Username: "alice", Records: map[string]*FileRecord{
				"hashA": {ContentHash: "hashA", Seq: 1},
			}},
		},
		ShardMap: map[string]*ShardLocations{
			"hashA": {Holders: map[int][]string{0: {"1", "203.0.113.5:51005"}}},
		},
	}
	if err := WriteNetworkManifest(dir, key, v3); err != nil {
		t.Fatalf("write v3: %v", err)
	}

	got, err := ReadNetworkManifest(dir, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Version != 4 {
		t.Errorf("version: got %d, want 4", got.Version)
	}
	if len(got.ShardMap) != 0 {
		t.Errorf("ShardMap should be cleared on v3→v4, got %v", got.ShardMap)
	}
	if got.Users[7] == nil || got.Users[7].Records["hashA"] == nil {
		t.Fatal("user file records must survive the migration")
	}
	if got.Users[7].Username != "alice" {
		t.Errorf("username should survive: got %q", got.Users[7].Username)
	}
}
