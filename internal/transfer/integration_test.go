package transfer

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This file is the transfer-pipeline integration harness. It exercises the parts
// of the file-transfer path that have no network dependency end-to-end:
//   - deterministic shard encryption (every copy of a shard is byte-identical)
//   - the storage-proof hash comparison across independently-produced copies
//   - Reed-Solomon repair regenerating byte-identical shards after loss
//
// The network transport (STUN/QUIC/TURN/TCP-relay) is deliberately out of scope
// here — it needs a live two-node environment — but everything from "bytes on
// disk" through reconstruction is covered.

// readShardFile reads the on-disk encrypted shard file for (fileHash, index).
func readShardFile(t *testing.T, fileHash string, index int) []byte {
	t.Helper()
	p := filepath.Join(ShardsDir(), fileHash, sprintfShard(index, fileHash))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read shard %d: %v", index, err)
	}
	return b
}

func sprintfShard(index int, fileHash string) string {
	return "shard" + itoa(index) + "_" + fileHash + ".dat"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ──────────────────────────────────────────────────────────
// Deterministic encryption
// ──────────────────────────────────────────────────────────

// TestDeterministicEncryption_ByteIdentical proves that two independent
// encryptions of the same shard produce byte-identical files. This is the fix
// that makes storage proofs work: before deterministic nonces, an uploader's
// local copy and a peer's copy of the same shard had different random nonces and
// therefore different bytes, so SHA-256(nonce‖bytes) differed and the probe
// falsely failed against an honest peer.
func TestDeterministicEncryption_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	key := testKey(0x42)
	fileHash := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

	plain := make([]byte, chunkSizeOnDisk*3+123) // spans multiple chunks
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "plain.dat")
	if err := os.WriteFile(src, plain, 0644); err != nil {
		t.Fatal(err)
	}

	a := filepath.Join(dir, "a.dat")
	b := filepath.Join(dir, "b.dat")
	if err := encryptAndStoreShardFile(src, a, key, fileHash, 3); err != nil {
		t.Fatalf("encrypt a: %v", err)
	}
	if err := encryptAndStoreShardFile(src, b, key, fileHash, 3); err != nil {
		t.Fatalf("encrypt b: %v", err)
	}

	ab, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ab, bb) {
		t.Fatal("same (key,fileHash,shardIndex) produced different bytes — encryption is not deterministic")
	}

	// A different shard index must produce different ciphertext (nonce domain
	// separation), otherwise identical plaintext across shards would collide.
	c := filepath.Join(dir, "c.dat")
	if err := encryptAndStoreShardFile(src, c, key, fileHash, 4); err != nil {
		t.Fatalf("encrypt c: %v", err)
	}
	cb, _ := os.ReadFile(c)
	if bytes.Equal(ab, cb) {
		t.Fatal("different shard indices produced identical bytes — nonce is not coordinate-separated")
	}
}

// TestStorageProof_HashMatchesAcrossCopies simulates the two ways a shard copy
// comes into existence — the uploader's local store (encryptAndStoreShardFile)
// and a peer's copy assembled from the wire (chunks encrypted the same way, then
// writeEncryptedShardFile) — and proves the storage-proof hash is identical for
// both, so an honest holder always passes the probe.
func TestStorageProof_HashMatchesAcrossCopies(t *testing.T) {
	base := useTempShardsDir(t)
	key := testKey(0x07)
	fileHash := "0011223344556677889900112233445566778899001122334455667788990011"
	shardIndex := 5

	plain := make([]byte, chunkSizeOnDisk*2+7)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "plain.dat")
	if err := os.WriteFile(src, plain, 0644); err != nil {
		t.Fatal(err)
	}

	// Uploader's local copy — written to the real ShardsDir layout so the probe
	// helper (which reads ShardsDir) can hash it.
	shardDir := filepath.Join(base, fileHash)
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		t.Fatal(err)
	}
	uploaderCopy := filepath.Join(shardDir, sprintfShard(shardIndex, fileHash))
	if err := encryptAndStoreShardFile(src, uploaderCopy, key, fileHash, shardIndex); err != nil {
		t.Fatalf("store uploader copy: %v", err)
	}

	// Peer's copy — encrypt each chunk exactly as the wire send path does, then
	// assemble with writeEncryptedShardFile exactly as finalizeShard does.
	var chunks [][]byte
	for off, ci := 0, 0; off < len(plain); off, ci = off+chunkSizeOnDisk, ci+1 {
		end := off + chunkSizeOnDisk
		if end > len(plain) {
			end = len(plain)
		}
		enc, err := encryptChunkDeterministic(key, fileHash, shardIndex, ci, plain[off:end])
		if err != nil {
			t.Fatalf("encrypt chunk %d: %v", ci, err)
		}
		chunks = append(chunks, enc)
	}
	peerCopy := filepath.Join(t.TempDir(), "peer.dat")
	if err := writeEncryptedShardFile(peerCopy, chunks); err != nil {
		t.Fatalf("write peer copy: %v", err)
	}

	ua, _ := os.ReadFile(uploaderCopy)
	pb, _ := os.ReadFile(peerCopy)
	if !bytes.Equal(ua, pb) {
		t.Fatal("uploader copy and wire-assembled peer copy differ — storage proof would false-fail")
	}

	// The probe hashes SHA-256(nonce ‖ shard_file). Both copies must hash equal.
	nonce := []byte("sixteen-byte-non")
	want, err := hashShardFileWithNonce(fileHash, shardIndex, nonce)
	if err != nil {
		t.Fatalf("hash uploader copy: %v", err)
	}
	// Point the probe helper at the peer copy by swapping it into ShardsDir.
	if err := os.WriteFile(uploaderCopy, pb, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := hashShardFileWithNonce(fileHash, shardIndex, nonce)
	if err != nil {
		t.Fatalf("hash peer copy: %v", err)
	}
	if want != got {
		t.Fatalf("probe hash mismatch across copies: %s vs %s", want, got)
	}
}

// ──────────────────────────────────────────────────────────
// Safe log truncation (remote-crash hardening)
// ──────────────────────────────────────────────────────────

// TestSafeTruncation_NoPanicOnShortInput guards the ShortHash/ShortPeer helpers
// used in log lines that format hashes and peer IDs pulled from untrusted peer
// messages. A raw slice like fileHash[:12] on a short/empty attacker-supplied
// value would panic in a handler goroutine and crash the daemon; these helpers
// must return safely instead.
func TestSafeTruncation_NoPanicOnShortInput(t *testing.T) {
	cases := []string{"", "a", "abc", "0011223344", "205.175.106.5:51005",
		"1122334455667788990011223344556677889900112233445566778899001122"}
	for _, in := range cases {
		if got := ShortHash(in); len(got) > 12 && len(got) != len(in) {
			t.Errorf("ShortHash(%q) = %q — wrong length", in, got)
		}
		if len(in) < 12 && ShortHash(in) != in {
			t.Errorf("ShortHash(%q) should pass short input through unchanged", in)
		}
		_ = ShortPeer(in) // must not panic on any input
	}
}

// TestDecodeBinaryShardChunk_RejectsHugeChunkCount guards against a remote
// memory-exhaustion DoS: a malicious frame claiming a huge totalChunks would
// otherwise drive make([]int, N) / make([][]byte, N) allocations of tens of GB.
// The decoder must reject an implausible count instead of accepting it.
func TestDecodeBinaryShardChunk_RejectsHugeChunkCount(t *testing.T) {
	fileHash := "1122334455667788990011223344556677889900112233445566778899001122"
	frame, err := encodeBinaryShardChunk(binaryShardChunk{
		fileHash:        fileHash,
		shardIndex:      0,
		chunkIndex:      0,
		totalChunks:     maxChunksPerShard + 1, // just over the bound
		totalDataShards: DataShards,
		totalShards:     TotalShards,
		data:            []byte("x"),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := decodeBinaryShardChunk(frame); err == nil {
		t.Fatal("decode should reject an implausible totalChunks, but accepted it")
	}

	// A legitimate count still decodes fine.
	ok, err := encodeBinaryShardChunk(binaryShardChunk{
		fileHash: fileHash, totalChunks: 1280, totalDataShards: DataShards,
		totalShards: TotalShards, data: []byte("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBinaryShardChunk(ok); err != nil {
		t.Fatalf("decode rejected a legitimate frame: %v", err)
	}
}

// TestGCStaleAssemblies verifies the memory backstop: a partially-received shard
// assembly that has gone idle past assemblyIdleTimeout is dropped, while a fresh
// one is kept. This bounds the memory a peer can hold with never-completing streams.
func TestGCStaleAssemblies(t *testing.T) {
	assemblyMu.Lock()
	for k := range assemblies { // clean slate for a deterministic check
		delete(assemblies, k)
	}
	assemblies["stalehash:0"] = &shardAssembly{lastChunkAt: time.Now().Add(-2 * assemblyIdleTimeout)}
	assemblies["freshhash:0"] = &shardAssembly{lastChunkAt: time.Now()}
	assemblyMu.Unlock()

	gcStaleAssemblies()

	assemblyMu.Lock()
	_, staleExists := assemblies["stalehash:0"]
	_, freshExists := assemblies["freshhash:0"]
	delete(assemblies, "freshhash:0") // cleanup so we don't leak into other tests
	assemblyMu.Unlock()

	if staleExists {
		t.Error("stale assembly should have been garbage-collected")
	}
	if !freshExists {
		t.Error("fresh assembly must survive GC")
	}
}

// ──────────────────────────────────────────────────────────
// Blind-courier filename privacy
// ──────────────────────────────────────────────────────────

// TestPrivacy_PeerMetaHasNoFilename verifies that a node storing a shard on
// behalf of another user never learns the file name or size: shard frames carry
// no identity, so a peer receiving one writes a privacy-stripped meta.json.
func TestPrivacy_PeerMetaHasNoFilename(t *testing.T) {
	useTempShardsDir(t)
	key := testKey(0x33)
	fileHash := "1122334455667788990011223344556677889900112233445566778899001122"

	// Build a single-chunk shard frame the way the (fixed) send path does — with
	// the file name and size omitted.
	enc, err := encryptChunkDeterministic(key, fileHash, 0, 0, []byte("shard payload bytes"))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := encodeBinaryShardChunk(binaryShardChunk{
		fileHash:        fileHash,
		shardIndex:      0,
		chunkIndex:      0,
		totalChunks:     1,
		totalDataShards: DataShards,
		totalShards:     TotalShards,
		data:            enc,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A peer must not be able to read a name out of the frame either.
	decoded, err := decodeBinaryShardChunk(frame)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.fileName != "" || decoded.fileSize != 0 {
		t.Fatalf("frame leaked identity: name=%q size=%d", decoded.fileName, decoded.fileSize)
	}

	HandleBinaryShardChunk(frame)

	// finalizeShard runs in a goroutine — wait for the meta.json to land.
	metaPath := filepath.Join(ShardsDir(), fileHash, "meta.json")
	var meta *ShardMeta
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(metaPath); err == nil {
			meta = FindShardMetaByHash(fileHash)
			if meta != nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if meta == nil {
		t.Fatal("meta.json was not written for the received shard")
	}
	if meta.FileName != "" {
		t.Errorf("peer meta leaked file name: %q", meta.FileName)
	}
	if meta.FileSize != 0 {
		t.Errorf("peer meta leaked file size: %d", meta.FileSize)
	}
}

// ──────────────────────────────────────────────────────────
// Repair
// ──────────────────────────────────────────────────────────

// TestRepair_RegeneratesByteIdenticalShards is the end-to-end durability test:
// upload a file (all 14 shards stored locally), snapshot them, delete several,
// run repair, and verify the regenerated shards are byte-identical to the
// originals and the file still reconstructs. This ties Task 1 (repair) and Task 2
// (deterministic encryption) together — repair only yields identical bytes
// because encryption is deterministic.
func TestRepair_RegeneratesByteIdenticalShards(t *testing.T) {
	uploadTestKey(t)
	useTempShardsDir(t)

	content := make([]byte, 200*1024) // multi-stripe file
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(t.TempDir(), "repairme.bin")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	fileHash, _, err := UploadFile(srcFile, nil)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	// Snapshot all shard files.
	original := make(map[int][]byte, TotalShards)
	for i := 0; i < TotalShards; i++ {
		original[i] = readShardFile(t, fileHash, i)
	}

	// Delete a mix of data and parity shards (indices 2, 5, 9, 12) — still ≥10 left.
	deleted := []int{2, 5, 9, 12}
	for _, i := range deleted {
		if err := os.Remove(filepath.Join(ShardsDir(), fileHash, sprintfShard(i, fileHash))); err != nil {
			t.Fatalf("delete shard %d: %v", i, err)
		}
	}

	repaired, err := RepairShardFile(fileHash)
	if err != nil {
		t.Fatalf("RepairShardFile: %v", err)
	}
	if len(repaired) != len(deleted) {
		t.Fatalf("repaired %v, expected %v", repaired, deleted)
	}

	// Each regenerated shard must be byte-identical to the original.
	for _, i := range deleted {
		got := readShardFile(t, fileHash, i)
		if !bytes.Equal(got, original[i]) {
			t.Fatalf("repaired shard %d is not byte-identical to the original (%d vs %d bytes)",
				i, len(got), len(original[i]))
		}
	}

	// The file must still reconstruct to the original bytes.
	got, err := FetchFileBytes("repairme.bin", nil, nil)
	if err != nil {
		t.Fatalf("FetchFileBytes after repair: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("reconstructed content mismatch after repair: got %d bytes, want %d", len(got), len(content))
	}
}

// TestRepair_NoopWithoutOwnerKey verifies a node that cannot decrypt the shards
// (wrong key → not the owner) does not attempt to regenerate them, leaving the
// missing shards missing rather than writing garbage.
func TestRepair_NoopWithoutOwnerKey(t *testing.T) {
	uploadTestKey(t)
	useTempShardsDir(t)

	content := make([]byte, 64*1024)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(t.TempDir(), "owned.bin")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}
	fileHash, _, err := UploadFile(srcFile, nil)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	// Delete a shard, then swap in a different shard key (simulating a non-owner).
	if err := os.Remove(filepath.Join(ShardsDir(), fileHash, sprintfShard(3, fileHash))); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	wrong := testKey(0x99)
	if err := os.WriteFile(filepath.Join(home, ".mosaic-shard.key"), wrong[:], 0600); err != nil {
		t.Fatal(err)
	}

	repaired, err := RepairShardFile(fileHash)
	if err != nil {
		t.Fatalf("RepairShardFile: unexpected error: %v", err)
	}
	if len(repaired) != 0 {
		t.Fatalf("non-owner should not repair, but regenerated %v", repaired)
	}
	if _, err := os.Stat(filepath.Join(ShardsDir(), fileHash, sprintfShard(3, fileHash))); err == nil {
		t.Fatal("shard 3 should still be missing — non-owner must not write it")
	}
}
