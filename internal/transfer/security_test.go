package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

// useShardKeyDir redirects shared.ShardKeyPath() to a temp dir for the
// duration of the test by overriding the HOME environment variable.
func useShardKeyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestShardEncryptionKey_LoadsFromDisk(t *testing.T) {
	home := useShardKeyDir(t)

	key := [32]byte{}
	for i := range key {
		key[i] = byte(i)
	}
	keyPath := filepath.Join(home, ".mosaic-shard.key")
	if err := os.WriteFile(keyPath, key[:], 0600); err != nil {
		t.Fatalf("write shard key: %v", err)
	}

	got, err := shardEncryptionKey()
	if err != nil {
		t.Fatalf("shardEncryptionKey: %v", err)
	}
	if got != key {
		t.Errorf("key mismatch: got %x, want %x", got, key)
	}
}

func TestShardEncryptionKey_MissingFile(t *testing.T) {
	useShardKeyDir(t) // home dir exists but no .mosaic-shard.key

	_, err := shardEncryptionKey()
	if err == nil {
		t.Fatal("expected error when shard key file is missing, got nil")
	}
}

func TestShardEncryptionKey_CorruptFile(t *testing.T) {
	home := useShardKeyDir(t)

	// Write fewer than 32 bytes — should be rejected.
	keyPath := filepath.Join(home, ".mosaic-shard.key")
	if err := os.WriteFile(keyPath, []byte("tooshort"), 0600); err != nil {
		t.Fatalf("write corrupt key: %v", err)
	}

	_, err := shardEncryptionKey()
	if err == nil {
		t.Fatal("expected error for corrupt shard key file, got nil")
	}
}

func TestShardEncryptionKey_Permissions(t *testing.T) {
	home := useShardKeyDir(t)

	key := [32]byte{0xDE, 0xAD}
	keyPath := filepath.Join(home, ".mosaic-shard.key")
	if err := os.WriteFile(keyPath, key[:], 0600); err != nil {
		t.Fatalf("write shard key: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions: got %o, want 0600", perm)
	}
}
