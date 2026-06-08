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

// ──────────────────────────────────────────────────────────
// RemoveShardHolder
// ──────────────────────────────────────────────────────────

func TestRemoveShardHolder_RemovesTargetNode(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "file1", 0, "node-A")
	RecordShardHolder(&m, "file1", 0, "node-B")
	RecordShardHolder(&m, "file1", 1, "node-A")

	changed := RemoveShardHolder(&m, "node-A")
	if !changed {
		t.Fatal("expected changed=true")
	}
	for _, shardIdx := range []int{0, 1} {
		for _, id := range GetShardHolders(m, "file1", shardIdx) {
			if id == "node-A" {
				t.Errorf("node-A still present in shard %d after removal", shardIdx)
			}
		}
	}
}

func TestRemoveShardHolder_PreservesOtherNodes(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "file1", 0, "node-A")
	RecordShardHolder(&m, "file1", 0, "node-B")
	RecordShardHolder(&m, "file1", 0, "node-C")

	RemoveShardHolder(&m, "node-A")

	holders := GetShardHolders(m, "file1", 0)
	found := map[string]bool{}
	for _, id := range holders {
		found[id] = true
		if id == "node-A" {
			t.Error("node-A should have been removed")
		}
	}
	if !found["node-B"] || !found["node-C"] {
		t.Errorf("node-B and node-C should still be present; got %v", holders)
	}
}

func TestRemoveShardHolder_ReturnsFalseWhenNotPresent(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "file1", 0, "node-A")

	changed := RemoveShardHolder(&m, "node-Z")
	if changed {
		t.Fatal("expected changed=false when node not present")
	}
}

func TestRemoveShardHolder_AcrossMultipleFiles(t *testing.T) {
	m := emptyManifest()
	RecordShardHolder(&m, "file1", 0, "node-X")
	RecordShardHolder(&m, "file2", 3, "node-X")
	RecordShardHolder(&m, "file2", 7, "node-X")
	RecordShardHolder(&m, "file2", 7, "node-Y")

	RemoveShardHolder(&m, "node-X")

	if len(GetShardHolders(m, "file1", 0)) != 0 {
		t.Error("file1 shard 0 should have no holders")
	}
	if len(GetShardHolders(m, "file2", 3)) != 0 {
		t.Error("file2 shard 3 should have no holders")
	}
	holders := GetShardHolders(m, "file2", 7)
	if len(holders) != 1 || holders[0] != "node-Y" {
		t.Errorf("file2 shard 7 should only have node-Y; got %v", holders)
	}
}

func TestRemoveShardHolder_EmptyMap(t *testing.T) {
	m := emptyManifest()
	changed := RemoveShardHolder(&m, "node-A")
	if changed {
		t.Fatal("expected changed=false on empty ShardMap")
	}
}
