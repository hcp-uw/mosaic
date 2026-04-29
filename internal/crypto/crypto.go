// Package crypto provides AES-GCM file encryption and per-user key
// wrapping so file keys can be sealed with the owner's identity.
//
// Wire format for an encrypted file: nonce (12 bytes) || ciphertext.
// The per-file symmetric key is wrapped with a key derived from the
// owner's ed25519 private key via HKDF-SHA-256, producing a 32-byte
// AES-256 key-wrapping key. Wrapped blobs are themselves AES-GCM
// sealed: nonce (12 bytes) || ciphertext.
//
// This design is sufficient for single-user MVP encryption — the owner
// can read their own files. Cross-user sharing (which would require an
// asymmetric handshake) is post-MVP.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// FileKeySize is the AES-256 key size.
const FileKeySize = 32

// NonceSize is the AES-GCM standard nonce size.
const NonceSize = 12

// keyWrapInfo binds derived wrap keys to this purpose, so that future
// uses of the same identity (e.g. signing) cannot be confused with key
// wrapping in cryptanalytic attacks.
const keyWrapInfo = "mosaic/v1 key-wrap"

// FileKey is a per-file AES-256 key.
type FileKey [FileKeySize]byte

// GenerateFileKey returns a fresh random file key.
func GenerateFileKey() (FileKey, error) {
	var k FileKey
	if _, err := io.ReadFull(rand.Reader, k[:]); err != nil {
		return FileKey{}, fmt.Errorf("read random key: %w", err)
	}
	return k, nil
}

// EncryptFile seals plaintext with AES-GCM and returns nonce||ciphertext.
func EncryptFile(key FileKey, plaintext []byte) ([]byte, error) {
	return aesGCMSeal(key[:], plaintext)
}

// DecryptFile reverses EncryptFile.
func DecryptFile(key FileKey, sealed []byte) ([]byte, error) {
	return aesGCMOpen(key[:], sealed)
}

// WrapKeyForOwner seals fileKey with a wrap key derived from the
// owner's ed25519 private key.
func WrapKeyForOwner(fileKey FileKey, ownerPriv ed25519.PrivateKey) ([]byte, error) {
	wrapKey, err := deriveWrapKey(ownerPriv)
	if err != nil {
		return nil, err
	}
	return aesGCMSeal(wrapKey, fileKey[:])
}

// UnwrapKey recovers a wrapped file key.
func UnwrapKey(wrapped []byte, ownerPriv ed25519.PrivateKey) (FileKey, error) {
	wrapKey, err := deriveWrapKey(ownerPriv)
	if err != nil {
		return FileKey{}, err
	}
	plain, err := aesGCMOpen(wrapKey, wrapped)
	if err != nil {
		return FileKey{}, err
	}
	if len(plain) != FileKeySize {
		return FileKey{}, fmt.Errorf("unwrapped key has size %d, want %d", len(plain), FileKeySize)
	}
	var fk FileKey
	copy(fk[:], plain)
	return fk, nil
}

func deriveWrapKey(priv ed25519.PrivateKey) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid ed25519 private key length")
	}
	// Use the seed (32 bytes of secret entropy) as the IKM. The public
	// key from the same identity acts as a stable per-user salt.
	seed := priv.Seed()
	pub := priv.Public().(ed25519.PublicKey)
	r := hkdf.New(sha256.New, seed, pub, []byte(keyWrapInfo))
	out := make([]byte, FileKeySize)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("derive wrap key: %w", err)
	}
	return out, nil
}

func aesGCMSeal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, NonceSize+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

func aesGCMOpen(key, sealed []byte) ([]byte, error) {
	if len(sealed) < NonceSize {
		return nil, errors.New("ciphertext too short")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, sealed[:NonceSize], sealed[NonceSize:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
