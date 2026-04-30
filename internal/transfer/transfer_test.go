package transfer

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// useTempShardsDir redirects all shard I/O to a t.TempDir() for the duration
// of the test, restoring the original value on cleanup.
func useTempShardsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := testShardsDir
	testShardsDir = dir
	t.Cleanup(func() { testShardsDir = prev })
	return dir
}

// ──────────────────────────────────────────────────────────
// StoreShardData
// ──────────────────────────────────────────────────────────

func TestStoreShardData_WritesFile(t *testing.T) {
	useTempShardsDir(t)
	hash := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	data := []byte("fake shard bytes")

	StoreShardData(hash, "test.txt", 1024, 0, DataShards, TotalShards, data)

	shardPath := filepath.Join(ShardsDir(), hash, fmt.Sprintf("shard0_%s.dat", hash))
	got, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("shard file not written: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("shard content mismatch: got %q, want %q", got, data)
	}
}

func TestStoreShardData_WritesMeta(t *testing.T) {
	useTempShardsDir(t)
	hash := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

	StoreShardData(hash, "test.txt", 2048, 3, DataShards, TotalShards, []byte("x"))

	metaPath := filepath.Join(ShardsDir(), hash, "meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("meta.json not written: %v", err)
	}
	var m ShardMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("meta.json invalid JSON: %v", err)
	}
	if m.FileName != "test.txt" {
		t.Errorf("FileName: got %q, want %q", m.FileName, "test.txt")
	}
	if m.FileHash != hash {
		t.Errorf("FileHash mismatch")
	}
	if m.FileSize != 2048 {
		t.Errorf("FileSize: got %d, want 2048", m.FileSize)
	}
	if m.TotalDataShards != DataShards {
		t.Errorf("TotalDataShards: got %d, want %d", m.TotalDataShards, DataShards)
	}
}

func TestStoreShardData_FiresCallback(t *testing.T) {
	useTempShardsDir(t)
	hash := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

	var (
		mu          sync.Mutex
		gotHash     string
		gotShard    int
		callbackHit = make(chan struct{}, 1)
	)
	SetShardStoredCallback(func(contentHash string, shardIndex int) {
		mu.Lock()
		gotHash = contentHash
		gotShard = shardIndex
		mu.Unlock()
		callbackHit <- struct{}{}
	})
	t.Cleanup(func() { SetShardStoredCallback(nil) })

	StoreShardData(hash, "test.txt", 512, 2, DataShards, TotalShards, []byte("y"))

	select {
	case <-callbackHit:
	default:
		// Give the goroutine a moment.
		select {
		case <-callbackHit:
		case <-timeoutChan(200):
			t.Fatal("shard-stored callback was not called within 200ms")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if gotHash != hash {
		t.Errorf("callback hash: got %q, want %q", gotHash, hash)
	}
	if gotShard != 2 {
		t.Errorf("callback shardIndex: got %d, want 2", gotShard)
	}
}

// ──────────────────────────────────────────────────────────
// FindShardMeta / FindShardMetaByHash
// ──────────────────────────────────────────────────────────

func TestFindShardMeta_Found(t *testing.T) {
	useTempShardsDir(t)
	hash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)
	meta := ShardMeta{FileName: "hello.txt", FileHash: hash, FileSize: 999, TotalDataShards: DataShards, TotalShards: TotalShards}
	raw, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(shardDir, "meta.json"), raw, 0644)

	got := FindShardMeta("hello.txt")
	if got == nil {
		t.Fatal("expected meta, got nil")
	}
	if got.FileHash != hash {
		t.Errorf("FileHash mismatch: got %q", got.FileHash)
	}
}

func TestFindShardMeta_NotFound(t *testing.T) {
	useTempShardsDir(t)
	if FindShardMeta("ghost.txt") != nil {
		t.Fatal("expected nil for unknown filename")
	}
}

func TestFindShardMetaByHash_Found(t *testing.T) {
	useTempShardsDir(t)
	hash := "cafecafecafecafecafecafecafecafecafecafecafecafecafecafecafecafe"

	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)
	meta := ShardMeta{FileName: "file.bin", FileHash: hash, FileSize: 42, TotalDataShards: DataShards, TotalShards: TotalShards}
	raw, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(shardDir, "meta.json"), raw, 0644)

	got := FindShardMetaByHash(hash)
	if got == nil {
		t.Fatal("expected meta, got nil")
	}
	if got.FileName != "file.bin" {
		t.Errorf("FileName: got %q, want file.bin", got.FileName)
	}
}

func TestFindShardMetaByHash_NotFound(t *testing.T) {
	useTempShardsDir(t)
	hash := "0000000000000000000000000000000000000000000000000000000000000000"
	if FindShardMetaByHash(hash) != nil {
		t.Fatal("expected nil for unknown hash")
	}
}

// ──────────────────────────────────────────────────────────
// missingDataShards
// ──────────────────────────────────────────────────────────

func TestMissingDataShards_AllMissing(t *testing.T) {
	useTempShardsDir(t)
	hash := "1111111111111111111111111111111111111111111111111111111111111111"
	os.MkdirAll(filepath.Join(ShardsDir(), hash), 0755)

	missing := missingDataShards(hash, 3)
	if len(missing) != 3 {
		t.Fatalf("expected 3 missing, got %v", missing)
	}
}

func TestMissingDataShards_SomePresent(t *testing.T) {
	useTempShardsDir(t)
	hash := "2222222222222222222222222222222222222222222222222222222222222222"
	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)

	// Write shards 0 and 2; leave 1 missing.
	os.WriteFile(filepath.Join(shardDir, fmt.Sprintf("shard0_%s.dat", hash)), []byte("a"), 0644)
	os.WriteFile(filepath.Join(shardDir, fmt.Sprintf("shard2_%s.dat", hash)), []byte("c"), 0644)

	missing := missingDataShards(hash, 3)
	if len(missing) != 1 || missing[0] != 1 {
		t.Fatalf("expected [1], got %v", missing)
	}
}

func TestMissingDataShards_NonePresent(t *testing.T) {
	useTempShardsDir(t)
	hash := "3333333333333333333333333333333333333333333333333333333333333333"
	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)

	for i := 0; i < 3; i++ {
		os.WriteFile(filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, hash)), []byte("x"), 0644)
	}

	missing := missingDataShards(hash, 3)
	if len(missing) != 0 {
		t.Fatalf("expected no missing shards, got %v", missing)
	}
}

// ──────────────────────────────────────────────────────────
// Encrypted shard file I/O
// ──────────────────────────────────────────────────────────

func testKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func TestEncryptedShardFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := testKey(0x01)

	// Build three plaintext chunks of different sizes.
	plaintexts := [][]byte{
		bytes.Repeat([]byte("A"), 100),
		bytes.Repeat([]byte("B"), 200),
		bytes.Repeat([]byte("C"), 50),
	}

	// Encrypt each chunk.
	var encChunks [][]byte
	for _, p := range plaintexts {
		enc, err := encryptChunk(key, p)
		if err != nil {
			t.Fatalf("encryptChunk: %v", err)
		}
		encChunks = append(encChunks, enc)
	}

	path := filepath.Join(dir, "shard.dat")
	if err := writeEncryptedShardFile(path, encChunks); err != nil {
		t.Fatalf("writeEncryptedShardFile: %v", err)
	}

	got, err := decryptShardToPlaintext(path, key)
	if err != nil {
		t.Fatalf("decryptShardToPlaintext: %v", err)
	}

	want := append(append(plaintexts[0], plaintexts[1]...), plaintexts[2]...)
	if !bytes.Equal(got, want) {
		t.Errorf("plaintext mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestEncryptedShardFile_WrongKey(t *testing.T) {
	dir := t.TempDir()
	writeKey := testKey(0xAA)
	wrongKey := testKey(0xBB)

	enc, _ := encryptChunk(writeKey, []byte("secret"))
	path := filepath.Join(dir, "shard.dat")
	_ = writeEncryptedShardFile(path, [][]byte{enc})

	_, err := decryptShardToPlaintext(path, wrongKey)
	if err == nil {
		t.Fatal("expected decryption error with wrong key, got nil")
	}
}

func TestEncryptShardFileToChunks(t *testing.T) {
	dir := t.TempDir()
	key := testKey(0x02)

	// Write a plaintext file slightly larger than one chunk.
	data := make([]byte, chunkSize+512)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "src.dat")
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	chunks, err := encryptShardFileToChunks(srcPath, key)
	if err != nil {
		t.Fatalf("encryptShardFileToChunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for %d-byte file, got %d", len(data), len(chunks))
	}

	// Round-trip: write then decrypt.
	outPath := filepath.Join(dir, "out.dat")
	if err := writeEncryptedShardFile(outPath, chunks); err != nil {
		t.Fatal(err)
	}
	got, err := decryptShardToPlaintext(outPath, key)
	if err != nil {
		t.Fatalf("decryptShardToPlaintext: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("round-trip mismatch: %d bytes in, %d bytes out", len(data), len(got))
	}
}

func TestDecryptShardsToDir_RightKey(t *testing.T) {
	useTempShardsDir(t)
	key := testKey(0x03)
	hash := "aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000aaaa0000"

	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)

	// Write 3 encrypted shards.
	want := map[int][]byte{
		0: bytes.Repeat([]byte("X"), 64),
		1: bytes.Repeat([]byte("Y"), 64),
		2: bytes.Repeat([]byte("Z"), 64),
	}
	for i, plain := range want {
		enc, _ := encryptChunk(key, plain)
		path := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, hash))
		_ = writeEncryptedShardFile(path, [][]byte{enc})
	}

	destDir := t.TempDir()
	n, err := decryptShardsToDir(hash, 3, key, destDir)
	if err != nil {
		t.Fatalf("decryptShardsToDir: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 decrypted, got %d", n)
	}

	for i, plain := range want {
		got, err := os.ReadFile(filepath.Join(destDir, hash, fmt.Sprintf("shard%d_%s.dat", i, hash)))
		if err != nil {
			t.Fatalf("shard %d not written: %v", i, err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("shard %d plaintext mismatch", i)
		}
	}
}

func TestDecryptShardsToDir_WrongKey(t *testing.T) {
	useTempShardsDir(t)
	writeKey := testKey(0x04)
	wrongKey := testKey(0x05)
	hash := "bbbb0000bbbb0000bbbb0000bbbb0000bbbb0000bbbb0000bbbb0000bbbb0000"

	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)

	for i := 0; i < 3; i++ {
		enc, _ := encryptChunk(writeKey, []byte("data"))
		path := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, hash))
		_ = writeEncryptedShardFile(path, [][]byte{enc})
	}

	destDir := t.TempDir()
	n, err := decryptShardsToDir(hash, 3, wrongKey, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 decrypted with wrong key, got %d", n)
	}
}

func TestDecryptShardsToDir_SkipsMissingShards(t *testing.T) {
	useTempShardsDir(t)
	key := testKey(0x06)
	hash := "cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000"

	shardDir := filepath.Join(ShardsDir(), hash)
	os.MkdirAll(shardDir, 0755)

	// Only write shards 0 and 2; shard 1 is missing.
	for _, i := range []int{0, 2} {
		enc, _ := encryptChunk(key, []byte("data"))
		path := filepath.Join(shardDir, fmt.Sprintf("shard%d_%s.dat", i, hash))
		_ = writeEncryptedShardFile(path, [][]byte{enc})
	}

	destDir := t.TempDir()
	n, err := decryptShardsToDir(hash, 3, key, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 decrypted, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(destDir, hash, fmt.Sprintf("shard1_%s.dat", hash))); err == nil {
		t.Error("shard 1 should not exist in dest dir")
	}
}

// ──────────────────────────────────────────────────────────
// EnsureShardMeta
// ──────────────────────────────────────────────────────────

func TestEnsureShardMeta_Creates(t *testing.T) {
	useTempShardsDir(t)
	hash := "dddd0000dddd0000dddd0000dddd0000dddd0000dddd0000dddd0000dddd0000"

	if FindShardMetaByHash(hash) != nil {
		t.Fatal("expected no meta before EnsureShardMeta")
	}

	EnsureShardMeta(hash, "notes.md", 4096)

	m := FindShardMetaByHash(hash)
	if m == nil {
		t.Fatal("expected meta after EnsureShardMeta, got nil")
	}
	if m.FileName != "notes.md" {
		t.Errorf("FileName: got %q, want notes.md", m.FileName)
	}
	if m.FileSize != 4096 {
		t.Errorf("FileSize: got %d, want 4096", m.FileSize)
	}
	if m.TotalDataShards != DataShards {
		t.Errorf("TotalDataShards: got %d, want %d", m.TotalDataShards, DataShards)
	}
	if m.BlockSize <= 0 {
		t.Errorf("BlockSize should be positive, got %d", m.BlockSize)
	}
}

func TestEnsureShardMeta_Idempotent(t *testing.T) {
	useTempShardsDir(t)
	hash := "eeee0000eeee0000eeee0000eeee0000eeee0000eeee0000eeee0000eeee0000"

	EnsureShardMeta(hash, "original.md", 1000)
	EnsureShardMeta(hash, "should-not-overwrite.md", 9999)

	m := FindShardMetaByHash(hash)
	if m == nil {
		t.Fatal("expected meta, got nil")
	}
	if m.FileName != "original.md" {
		t.Errorf("second call should not overwrite: got %q", m.FileName)
	}
}

// ──────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────

func timeoutChan(ms int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		// crude sleep via a channel — avoids importing time in a helper
		// that also needs to compile without side effects.
		c := make(chan struct{})
		go func() {
			// spin-wait is fine for short test timeouts
			for i := 0; i < ms*1000; i++ {
			}
			close(c)
		}()
		<-c
		close(ch)
	}()
	return ch
}
