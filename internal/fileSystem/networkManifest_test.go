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

func makeTestChain(t *testing.T, userID int, kp UserKeyPair) UserChain {
	t.Helper()
	pubBytes, err := PublicKeyBytes(kp.Public)
	if err != nil {
		t.Fatalf("serialize public key: %v", err)
	}
	return UserChain{UserID: userID, Username: "testuser", PublicKey: pubBytes}
}

func testFile(name string) NetworkFileEntry {
	return NetworkFileEntry{Name: name, Size: 100, PrimaryNodeID: 1, ContentHash: "deadbeef"}
}

// ──────────────────────────────────────────────────────────
// ValidateChain
// ──────────────────────────────────────────────────────────

func TestValidateChain_Empty(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	if !ValidateChain(chain) {
		t.Fatal("empty chain should be valid")
	}
}

func TestValidateChain_ValidSingleBlock(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	if err := AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp); err != nil {
		t.Fatalf("AppendBlock: %v", err)
	}
	if !ValidateChain(chain) {
		t.Fatal("chain with one valid block should be valid")
	}
}

func TestValidateChain_ValidMultiBlock(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := AppendBlock(&chain, BlockOpAdd, testFile(name), "", kp); err != nil {
			t.Fatalf("AppendBlock: %v", err)
		}
	}
	if !ValidateChain(chain) {
		t.Fatal("multi-block chain should be valid")
	}
}

func TestValidateChain_TamperedBlockContent(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	if err := AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp); err != nil {
		t.Fatal(err)
	}
	// Silently mutate the file name after signing — signature should no longer match.
	chain.Blocks[0].File.Name = "evil.txt"
	if ValidateChain(chain) {
		t.Fatal("chain with tampered block content should be invalid")
	}
}

func TestValidateChain_TamperedSignature(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	if err := AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp); err != nil {
		t.Fatal(err)
	}
	chain.Blocks[0].Signature[0] ^= 0xFF // flip bits in signature
	if ValidateChain(chain) {
		t.Fatal("chain with corrupted signature should be invalid")
	}
}

func TestValidateChain_BrokenPrevHash(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp)
	AppendBlock(&chain, BlockOpAdd, testFile("b.txt"), "", kp)
	// Break the PrevHash link on block 1.
	chain.Blocks[1].PrevHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if ValidateChain(chain) {
		t.Fatal("chain with broken PrevHash link should be invalid")
	}
}

func TestValidateChain_WrongPublicKey(t *testing.T) {
	kp1 := makeTestKeyPair(t)
	kp2 := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp1)
	AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp1)
	// Swap in a different public key — signatures should no longer verify.
	pubBytes, _ := PublicKeyBytes(kp2.Public)
	chain.PublicKey = pubBytes
	if ValidateChain(chain) {
		t.Fatal("chain validated with wrong public key")
	}
}

// ──────────────────────────────────────────────────────────
// ChainToFiles
// ──────────────────────────────────────────────────────────

func TestChainToFiles_AddRemove(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp)
	AppendBlock(&chain, BlockOpAdd, testFile("b.txt"), "", kp)
	AppendBlock(&chain, BlockOpRemove, testFile("a.txt"), "", kp)

	files := ChainToFiles(chain)
	if len(files) != 1 || files[0].Name != "b.txt" {
		t.Fatalf("expected [b.txt], got %v", files)
	}
}

func TestChainToFiles_Rename(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	AppendBlock(&chain, BlockOpAdd, testFile("old.txt"), "", kp)
	AppendBlock(&chain, BlockOpRename, testFile("old.txt"), "new.txt", kp)

	files := ChainToFiles(chain)
	if len(files) != 1 || files[0].Name != "new.txt" {
		t.Fatalf("expected [new.txt], got %v", files)
	}
}

func TestChainToFiles_RemoveAll(t *testing.T) {
	kp := makeTestKeyPair(t)
	chain := makeTestChain(t, 1, kp)
	AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp)
	AppendBlock(&chain, BlockOpRemove, testFile("a.txt"), "", kp)

	files := ChainToFiles(chain)
	if len(files) != 0 {
		t.Fatalf("expected empty file set, got %v", files)
	}
}

// ──────────────────────────────────────────────────────────
// MergeNetworkManifest / pickBetterChain
// ──────────────────────────────────────────────────────────

func TestMerge_NewUserFromRemote(t *testing.T) {
	kp := makeTestKeyPair(t)

	local := NetworkManifest{Version: 2, Chains: []UserChain{}}
	remote := NetworkManifest{Version: 2, Chains: []UserChain{makeTestChain(t, 42, kp)}}
	AppendBlock(&remote.Chains[0], BlockOpAdd, testFile("a.txt"), "", kp)

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true when a new user chain is merged in")
	}
	if FindChainIndex(merged, 42) == -1 {
		t.Fatal("user 42 should exist in merged manifest")
	}
}

func TestMerge_LongerRemoteWins(t *testing.T) {
	kp := makeTestKeyPair(t)

	localChain := makeTestChain(t, 1, kp)
	AppendBlock(&localChain, BlockOpAdd, testFile("a.txt"), "", kp)

	remoteChain := makeTestChain(t, 1, kp)
	AppendBlock(&remoteChain, BlockOpAdd, testFile("a.txt"), "", kp)
	AppendBlock(&remoteChain, BlockOpAdd, testFile("b.txt"), "", kp)

	local := NetworkManifest{Version: 2, Chains: []UserChain{localChain}}
	remote := NetworkManifest{Version: 2, Chains: []UserChain{remoteChain}}

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		t.Fatal("expected changed=true when remote chain is longer")
	}
	idx := FindChainIndex(merged, 1)
	if len(merged.Chains[idx].Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(merged.Chains[idx].Blocks))
	}
}

func TestMerge_LocalLongerKept(t *testing.T) {
	kp := makeTestKeyPair(t)

	localChain := makeTestChain(t, 1, kp)
	AppendBlock(&localChain, BlockOpAdd, testFile("a.txt"), "", kp)
	AppendBlock(&localChain, BlockOpAdd, testFile("b.txt"), "", kp)

	remoteChain := makeTestChain(t, 1, kp)
	AppendBlock(&remoteChain, BlockOpAdd, testFile("a.txt"), "", kp)

	local := NetworkManifest{Version: 2, Chains: []UserChain{localChain}}
	remote := NetworkManifest{Version: 2, Chains: []UserChain{remoteChain}}

	merged, changed := MergeNetworkManifest(local, remote)
	if changed {
		t.Fatal("expected changed=false when local chain is already longer")
	}
	idx := FindChainIndex(merged, 1)
	if len(merged.Chains[idx].Blocks) != 2 {
		t.Fatalf("expected 2 blocks to be kept, got %d", len(merged.Chains[idx].Blocks))
	}
}

func TestMerge_InvalidRemoteChainDropped(t *testing.T) {
	kp := makeTestKeyPair(t)

	badChain := makeTestChain(t, 99, kp)
	AppendBlock(&badChain, BlockOpAdd, testFile("a.txt"), "", kp)
	badChain.Blocks[0].File.Name = "evil.txt" // tamper after signing

	local := NetworkManifest{Version: 2, Chains: []UserChain{}}
	remote := NetworkManifest{Version: 2, Chains: []UserChain{badChain}}

	merged, changed := MergeNetworkManifest(local, remote)
	if changed {
		t.Fatal("expected changed=false — invalid chain should be silently dropped")
	}
	if FindChainIndex(merged, 99) != -1 {
		t.Fatal("invalid chain should not be in merged manifest")
	}
}

func TestMerge_IdenticalChains(t *testing.T) {
	kp := makeTestKeyPair(t)

	chain := makeTestChain(t, 1, kp)
	AppendBlock(&chain, BlockOpAdd, testFile("a.txt"), "", kp)

	local := NetworkManifest{Version: 2, Chains: []UserChain{chain}}
	remote := NetworkManifest{Version: 2, Chains: []UserChain{chain}}

	_, changed := MergeNetworkManifest(local, remote)
	if changed {
		t.Fatal("expected changed=false for identical chains")
	}
}

// ──────────────────────────────────────────────────────────
// BlockHash determinism — signature must not affect hash
// ──────────────────────────────────────────────────────────

func TestBlockHash_SignatureExcluded(t *testing.T) {
	kp := makeTestKeyPair(t)
	b := ChainBlock{
		Index:    0,
		PrevHash: "",
		Op:       BlockOpAdd,
		File:     testFile("a.txt"),
	}

	h1, _ := BlockHash(b)

	// Sign the block and re-hash — result must be the same.
	signBlock(&b, kp.Private)
	h2, _ := BlockHash(b)

	if h1 != h2 {
		t.Fatalf("BlockHash changed after signing: %s != %s", h1, h2)
	}
}

// ──────────────────────────────────────────────────────────
// ShardMap — RecordShardHolder / GetShardHolders / merge
// ──────────────────────────────────────────────────────────

func emptyManifest() NetworkManifest {
	return NetworkManifest{Version: 2, Chains: []UserChain{}, ShardMap: make(map[string]*ShardLocations)}
}

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
	changed := RecordShardHolder(&m, "abc123", 0, "node-A") // same again
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
	RecordShardHolder(&remote, "abc123", 0, "node-B") // new holder, same shard
	RecordShardHolder(&remote, "abc123", 1, "node-C") // new shard entirely

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

	// Merging an identical shard map should not report changed.
	_, changed := MergeNetworkManifest(m, m)
	if changed {
		t.Fatal("expected changed=false when shard maps are identical")
	}
}

// ──────────────────────────────────────────────────────────
// AppendBlockAdd / Remove / Rename manifest helpers
// ──────────────────────────────────────────────────────────

func TestAppendBlockAdd_CreatesChain(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := NetworkManifest{Version: 2, Chains: []UserChain{}}

	if err := AppendBlockAdd(&m, 1, "alice", testFile("a.txt"), kp); err != nil {
		t.Fatalf("AppendBlockAdd: %v", err)
	}

	idx := FindChainIndex(m, 1)
	if idx == -1 {
		t.Fatal("user 1 not found after AppendBlockAdd")
	}
	if !ValidateChain(m.Chains[idx]) {
		t.Fatal("chain should be valid after AppendBlockAdd")
	}
}

func TestAppendBlockRemove_MissingUserErrors(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := NetworkManifest{Version: 2, Chains: []UserChain{}}

	err := AppendBlockRemove(&m, 999, "a.txt", kp)
	if err == nil {
		t.Fatal("expected error removing from nonexistent user chain")
	}
}

func TestFullLifecycle(t *testing.T) {
	kp := makeTestKeyPair(t)
	m := NetworkManifest{Version: 2, Chains: []UserChain{}}

	AppendBlockAdd(&m, 1, "alice", testFile("a.txt"), kp)
	AppendBlockAdd(&m, 1, "alice", testFile("b.txt"), kp)
	AppendBlockRename(&m, 1, "a.txt", "renamed.txt", kp)
	AppendBlockRemove(&m, 1, "b.txt", kp)

	idx := FindChainIndex(m, 1)
	if !ValidateChain(m.Chains[idx]) {
		t.Fatal("chain should be valid after full lifecycle")
	}

	files := ChainToFiles(m.Chains[idx])
	if len(files) != 1 || files[0].Name != "renamed.txt" {
		t.Fatalf("expected [renamed.txt], got %v", files)
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

	// node-A should be gone from both shards.
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
	for _, id := range holders {
		if id == "node-A" {
			t.Error("node-A should have been removed")
		}
	}
	found := map[string]bool{}
	for _, id := range holders {
		found[id] = true
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
