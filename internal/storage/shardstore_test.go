package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func hashOf(b []byte) Hash {
	sum := sha256.Sum256(b)
	var h Hash
	copy(h[:], sum[:])
	return h
}

func TestShardStore_PutGetRoundTrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data := []byte("the quick brown fox")
	h := hashOf(data)

	if err := s.Put(h, data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !s.Has(h) {
		t.Error("Has returned false after Put")
	}

	got, err := s.Get(h)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Get returned %q, want %q", got, data)
	}
}

func TestShardStore_TwoLevelLayout(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data := []byte("layout check")
	h := hashOf(data)
	if err := s.Put(h, data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	want := filepath.Join(dir, h.Hex()[:2], h.Hex()+".dat")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected shard at %s: %v", want, err)
	}
}

func TestShardStore_Delete(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := hashOf([]byte("delete me"))
	if err := s.Put(h, []byte("delete me")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Has(h) {
		t.Error("Has returned true after Delete")
	}
	if _, err := s.Get(h); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete returned %v, want ErrNotFound", err)
	}
	if err := s.Delete(h); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete returned %v, want ErrNotFound", err)
	}
}

func TestShardStore_GetMissing(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Get(hashOf([]byte("nope"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestShardStore_UsedBytesTracking(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := bytes.Repeat([]byte{1}, 1024)
	b := bytes.Repeat([]byte{2}, 2048)
	if err := s.Put(hashOf(a), a); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := s.Put(hashOf(b), b); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if got, want := s.UsedBytes(), uint64(1024+2048); got != want {
		t.Errorf("UsedBytes = %d, want %d", got, want)
	}
	if err := s.Delete(hashOf(a)); err != nil {
		t.Fatalf("Delete a: %v", err)
	}
	if got, want := s.UsedBytes(), uint64(2048); got != want {
		t.Errorf("UsedBytes after delete = %d, want %d", got, want)
	}
}

func TestShardStore_PersistsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data := bytes.Repeat([]byte{7}, 4096)
	h := hashOf(data)
	if err := s1.Put(h, data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	if !s2.Has(h) {
		t.Error("expected shard to persist across reopens")
	}
	if got, want := s2.UsedBytes(), uint64(len(data)); got != want {
		t.Errorf("UsedBytes after reopen = %d, want %d", got, want)
	}
}

func TestShardStore_List(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := map[string]bool{}
	for _, b := range [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")} {
		h := hashOf(b)
		if err := s.Put(h, b); err != nil {
			t.Fatalf("Put: %v", err)
		}
		want[h.Hex()] = true
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("List returned %d shards, want %d", len(got), len(want))
	}
	for _, h := range got {
		if !want[h.Hex()] {
			t.Errorf("unexpected hash %s in List", h.Hex())
		}
	}
}

func TestShardStore_ConcurrentPutDistinctHashes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	hashes := make([]Hash, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			data := make([]byte, 256)
			if _, err := rand.Read(data); err != nil {
				t.Errorf("rand: %v", err)
				return
			}
			h := hashOf(data)
			hashes[i] = h
			if err := s.Put(h, data); err != nil {
				t.Errorf("Put: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, h := range hashes {
		if !s.Has(h) {
			t.Errorf("missing shard %s after concurrent Put", h.Hex())
		}
	}
}

func TestNew_RejectsEmptyDir(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected error for empty dir")
	}
}
