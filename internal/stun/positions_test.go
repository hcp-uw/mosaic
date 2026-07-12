package stun

import (
	"path/filepath"
	"testing"
)

// TestPositionStore_PersistsAndRestores is the core of the queue-position
// persistence fix: positions written by one server instance must be reclaimable
// by a fresh instance loading the same file — this is what lets a node keep its
// original position across a STUN restart instead of getting a fresh one.
func TestPositionStore_PersistsAndRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "positions.json")

	s1 := loadPositionStore(path)
	s1.put("node-a", 1)
	s1.put("node-b", 2)
	s1.put("node-c", 3)

	// A brand-new store loading the same file must see all three positions.
	s2 := loadPositionStore(path)
	for id, want := range map[string]int{"node-a": 1, "node-b": 2, "node-c": 3} {
		got, ok := s2.get(id)
		if !ok || got != want {
			t.Fatalf("restored position for %s: got (%d,%v), want %d", id, got, ok, want)
		}
	}
	if got := s2.maxPosition(); got != 3 {
		t.Fatalf("maxPosition after restore: got %d, want 3", got)
	}
	if _, ok := s2.get("node-unknown"); ok {
		t.Fatal("unknown node should not have a stored position")
	}
}

// TestPositionStore_InMemoryWhenNoPath verifies the store works without a path
// (persistence disabled) and does not panic on writes.
func TestPositionStore_InMemoryWhenNoPath(t *testing.T) {
	s := loadPositionStore("")
	s.put("node-a", 5)
	if got, ok := s.get("node-a"); !ok || got != 5 {
		t.Fatalf("in-memory get: got (%d,%v), want 5", got, ok)
	}
	if got := s.maxPosition(); got != 5 {
		t.Fatalf("in-memory maxPosition: got %d, want 5", got)
	}
}
