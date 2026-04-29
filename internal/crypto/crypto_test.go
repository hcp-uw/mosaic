package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func mustEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func TestFileEncryptDecrypt_RoundTrip(t *testing.T) {
	key, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("GenerateFileKey: %v", err)
	}
	plain := []byte("Mosaic encrypts files end-to-end.")
	sealed, err := EncryptFile(key, plain)
	if err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	got, err := DecryptFile(key, sealed)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("DecryptFile = %q, want %q", got, plain)
	}
}

func TestFileEncrypt_TamperRejected(t *testing.T) {
	key, _ := GenerateFileKey()
	sealed, err := EncryptFile(key, []byte("hello"))
	if err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	sealed[len(sealed)-1] ^= 0x01
	if _, err := DecryptFile(key, sealed); err == nil {
		t.Fatal("expected DecryptFile to reject tampered ciphertext")
	}
}

func TestDecryptFile_TooShort(t *testing.T) {
	key, _ := GenerateFileKey()
	if _, err := DecryptFile(key, []byte("short")); err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestKeyWrap_RoundTrip(t *testing.T) {
	_, priv := mustEd25519(t)
	fileKey, _ := GenerateFileKey()

	wrapped, err := WrapKeyForOwner(fileKey, priv)
	if err != nil {
		t.Fatalf("WrapKeyForOwner: %v", err)
	}
	got, err := UnwrapKey(wrapped, priv)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	if got != fileKey {
		t.Error("unwrapped file key does not match original")
	}
}

func TestKeyWrap_DifferentOwnerRejected(t *testing.T) {
	_, owner := mustEd25519(t)
	_, other := mustEd25519(t)

	fileKey, _ := GenerateFileKey()
	wrapped, err := WrapKeyForOwner(fileKey, owner)
	if err != nil {
		t.Fatalf("WrapKeyForOwner: %v", err)
	}
	if _, err := UnwrapKey(wrapped, other); err == nil {
		t.Fatal("expected unwrap with different owner to fail")
	}
}

func TestKeyWrap_DeterministicAcrossCalls(t *testing.T) {
	// Wrapping is randomized (fresh nonce), so blobs differ — but both
	// must unwrap to the same file key.
	_, priv := mustEd25519(t)
	fileKey, _ := GenerateFileKey()

	a, err := WrapKeyForOwner(fileKey, priv)
	if err != nil {
		t.Fatalf("wrap a: %v", err)
	}
	b, err := WrapKeyForOwner(fileKey, priv)
	if err != nil {
		t.Fatalf("wrap b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two wrap operations produced identical ciphertext (no nonce randomness)")
	}
	gotA, _ := UnwrapKey(a, priv)
	gotB, _ := UnwrapKey(b, priv)
	if gotA != fileKey || gotB != fileKey {
		t.Error("wrap/unwrap did not preserve file key")
	}
}

func TestEncrypt_LargePlaintext(t *testing.T) {
	key, _ := GenerateFileKey()
	plain := make([]byte, 1<<16)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	sealed, err := EncryptFile(key, plain)
	if err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	got, err := DecryptFile(key, sealed)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Error("large round-trip mismatch")
	}
}
