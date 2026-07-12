package handlers

import "testing"

func TestSuppressHolder(t *testing.T) {
	// A holder we haven't disproven is trusted.
	if IsHolderSuppressed("node-A", "hashX", 3) {
		t.Fatal("holder should not be suppressed before any proof")
	}

	SuppressHolder("node-A", "hashX", 3)

	// Now suppressed for that exact (holder, file, shard).
	if !IsHolderSuppressed("node-A", "hashX", 3) {
		t.Fatal("holder should be suppressed after SuppressHolder")
	}
	// Suppression is scoped: a different shard/file/holder is unaffected.
	if IsHolderSuppressed("node-A", "hashX", 4) {
		t.Error("suppression must be per-shard")
	}
	if IsHolderSuppressed("node-A", "hashY", 3) {
		t.Error("suppression must be per-file")
	}
	if IsHolderSuppressed("node-B", "hashX", 3) {
		t.Error("suppression must be per-holder")
	}

	// Empty holder id is a no-op (never recorded, never matches).
	SuppressHolder("", "hashX", 3)
	if IsHolderSuppressed("", "hashX", 3) {
		t.Error("empty holder id must not be suppressed")
	}
}
