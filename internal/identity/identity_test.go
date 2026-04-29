package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreate_GeneratesNewIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	id, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(id.Private) != ed25519.PrivateKeySize {
		t.Fatalf("private key size %d, want %d", len(id.Private), ed25519.PrivateKeySize)
	}
	if len(id.Public) != ed25519.PublicKeySize {
		t.Fatalf("public key size %d, want %d", len(id.Public), ed25519.PublicKeySize)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("identity file perm = %o, want 0600", perm)
	}
}

func TestLoadOrCreate_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}

	if first.PublicKeyHex() != second.PublicKeyHex() {
		t.Error("expected identical identity across LoadOrCreate calls")
	}
}

func TestLoadOrCreate_RejectsBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("expected error for short identity file")
	}
}

func TestLoadOrCreate_EmptyPath(t *testing.T) {
	if _, err := LoadOrCreate(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestPublicKeyHex_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreate(filepath.Join(dir, "id.key"))
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	decoded, err := PublicKeyFromHex(id.PublicKeyHex())
	if err != nil {
		t.Fatalf("PublicKeyFromHex: %v", err)
	}
	if string(decoded) != string(id.Public) {
		t.Error("decoded public key does not match original")
	}
}

func TestPublicKeyFromHex_BadInput(t *testing.T) {
	if _, err := PublicKeyFromHex("not-hex"); err == nil {
		t.Error("expected error for non-hex string")
	}
	if _, err := PublicKeyFromHex("00ff"); err == nil {
		t.Error("expected error for short hex string")
	}
}
