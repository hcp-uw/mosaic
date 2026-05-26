package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const mosaicKeyFile = ".mosaic-key"

func mosaicKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Mosaic", mosaicKeyFile)
}

func writeUserKey(raw string) error {
	keyPath := mosaicKeyPath()
	if keyPath == "" {
		return fmt.Errorf("cannot resolve user home for %s", mosaicKeyFile)
	}
	base := filepath.Dir(keyPath)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("key cannot be empty")
	}
	return os.WriteFile(keyPath, []byte(trimmed+"\n"), 0o600)
}

func loadIdentityAndKey() (string, []byte, error) {
	keyPath := mosaicKeyPath()
	if keyPath == "" {
		return "", nil, fmt.Errorf("cannot resolve user home for %s", mosaicKeyFile)
	}
	b, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("missing %s; set a key with: client -set-key \"<secret>\"", keyPath)
		}
		return "", nil, err
	}
	userKey := strings.TrimSpace(string(b))
	if userKey == "" {
		return "", nil, fmt.Errorf("%s is empty; set a key with: client -set-key \"<secret>\"", keyPath)
	}
	return deriveIdentityAndKey(userKey), deriveEncryptionKey(userKey), nil
}

func deriveIdentityAndKey(userKey string) string {
	sum := sha256.Sum256([]byte("mosaic-identity:" + userKey))
	return hex.EncodeToString(sum[:16])
}

func deriveEncryptionKey(userKey string) []byte {
	sum := sha256.Sum256([]byte("mosaic-encryption:" + userKey))
	return sum[:]
}

func encryptData(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ciphertext))
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

func decryptData(key, payload []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := aead.NonceSize()
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	return aead.Open(nil, nonce, ciphertext, nil)
}

func encryptFilenameToken(key []byte, filename string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := deterministicFilenameNonce(key, filename, aead.NonceSize())
	ciphertext := aead.Seal(nil, nonce, []byte(filename), nil)
	out := make([]byte, 0, len(nonce)+len(ciphertext))
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

func decryptFilenameToken(key []byte, token string) (string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aead.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("filename token too short")
	}
	plain, err := aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func deterministicFilenameNonce(key []byte, filename string, n int) []byte {
	sum := sha256.Sum256(append([]byte("mosaic-filename-nonce:"+filename+":"), key...))
	out := make([]byte, n)
	copy(out, sum[:])
	return out
}
