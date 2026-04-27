package fileSystem

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const networkManifestFilename = ".mosaic-network-manifest"

// networkManifestMu serializes disk read-modify-write cycles.
var networkManifestMu sync.Mutex

// NetworkFileEntry is the per-file record stored in the network manifest.
type NetworkFileEntry struct {
	Name          string `json:"name"`
	Size          int    `json:"size"`
	PrimaryNodeID int    `json:"primaryNodeID"`
	DateAdded     string `json:"dateAdded"`
	ContentHash   string `json:"contentHash"` // SHA-256 hex of original file
}

// UserNetworkEntry groups all files owned by one user.
//
// Security model (two independent layers):
//
//  1. Per-user ECIES encryption: Files is encrypted using ECDH + AES-256-GCM
//     with the user's ECDSA P-256 public key as the recipient key.
//     EphemeralPubKey carries the sender's ephemeral public key so the owner
//     can re-derive the shared secret and decrypt. No other node can read
//     another user's file list — they don't have the private key.
//
//  2. Ciphertext signature: Signature is an ECDSA sig over
//     SHA-256(EphemeralPubKey || EncryptedFiles), signed with the owner's
//     private key. Any peer can verify this using the embedded PublicKey,
//     without decrypting. Tampered ciphertext → invalid signature → dropped.
//
// Files is populated in memory after DecryptUserFiles; it is never serialized
// (json:"-"). Peers holding this manifest see only opaque EncryptedFiles bytes.
type UserNetworkEntry struct {
	UserID          int    `json:"userID"`
	Username        string `json:"username"`
	PublicKey       []byte `json:"publicKey"`       // ECDSA P-256, PKIX DER
	EphemeralPubKey []byte `json:"ephemeralPubKey"` // ECIES sender ephemeral public key
	EncryptedFiles  []byte `json:"encryptedFiles"`  // ECIES ciphertext of json(Files)
	Signature       []byte `json:"signature"`       // ECDSA r||s over SHA-256(EphemeralPubKey||EncryptedFiles)
	UpdatedAt       string `json:"updatedAt"`       // RFC3339 UTC; set on each sign so merge can compare per-user

	// In-memory only. Populated by DecryptUserFiles; never written to disk.
	Files []NetworkFileEntry `json:"-"`
}

// NetworkManifest is the root structure written to disk after encryption.
// Entries MUST remain sorted by UserID at all times.
type NetworkManifest struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updatedAt"`
	Entries   []UserNetworkEntry `json:"entries"`
}

// networkManifestPath returns the path to the on-disk manifest file.
func networkManifestPath(mosaicDir string) string {
	return filepath.Join(mosaicDir, networkManifestFilename)
}

// ──────────────────────────────────────────────────────────
// Disk I/O
// ──────────────────────────────────────────────────────────

// ReadNetworkManifest decrypts and deserializes the manifest from disk.
// Returns an empty manifest if the file does not exist.
// Note: Files fields are empty for all entries — call DecryptUserFiles to
// populate your own entry before mutating.
func ReadNetworkManifest(mosaicDir string, key [32]byte) (NetworkManifest, error) {
	empty := NetworkManifest{Version: 1, Entries: []UserNetworkEntry{}}

	data, err := os.ReadFile(networkManifestPath(mosaicDir))
	if os.IsNotExist(err) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("could not read network manifest: %w", err)
	}

	plaintext, err := decryptAESGCM(key, data)
	if err != nil {
		return empty, fmt.Errorf("could not decrypt network manifest: %w", err)
	}

	var m NetworkManifest
	if err := json.Unmarshal(plaintext, &m); err != nil {
		return empty, fmt.Errorf("could not parse network manifest: %w", err)
	}

	return m, nil
}

// WriteNetworkManifest serializes, encrypts, and atomically writes the manifest.
// Sets UpdatedAt to current UTC time before writing.
// Files fields are intentionally excluded from JSON (json:"-").
func WriteNetworkManifest(mosaicDir string, key [32]byte, m NetworkManifest) error {
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("could not marshal network manifest: %w", err)
	}

	ciphertext, err := encryptAESGCM(key, data)
	if err != nil {
		return fmt.Errorf("could not encrypt network manifest: %w", err)
	}

	tmp := networkManifestPath(mosaicDir) + ".tmp"
	if err := os.WriteFile(tmp, ciphertext, 0600); err != nil {
		return fmt.Errorf("could not write network manifest: %w", err)
	}

	return os.Rename(tmp, networkManifestPath(mosaicDir))
}

// ReadAndDecryptNetworkManifest is a convenience wrapper: reads the manifest
// from disk and decrypts the given user's Files section in one call.
func ReadAndDecryptNetworkManifest(mosaicDir string, aesKey [32]byte, userID int, priv *ecdsa.PrivateKey) (NetworkManifest, error) {
	m, err := ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		return m, err
	}
	i := FindUserIndex(m, userID)
	if i != -1 {
		if derr := DecryptUserFiles(&m.Entries[i], priv); derr != nil {
			// Non-fatal: entry may be new and have no ciphertext yet.
			m.Entries[i].Files = []NetworkFileEntry{}
		}
	}
	return m, nil
}

// EncryptSignAndWriteNetworkManifest encrypts the user's Files, signs, and
// writes the manifest to disk atomically.
func EncryptSignAndWriteNetworkManifest(mosaicDir string, aesKey [32]byte, m NetworkManifest, userID int, kp UserKeyPair) error {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	i := FindUserIndex(m, userID)
	if i != -1 {
		if err := EncryptAndSignUserEntry(&m.Entries[i], kp); err != nil {
			return fmt.Errorf("could not encrypt/sign entry: %w", err)
		}
	}
	return WriteNetworkManifest(mosaicDir, aesKey, m)
}

// ──────────────────────────────────────────────────────────
// Binary search helpers
// ──────────────────────────────────────────────────────────

// FindUserIndex returns the index in m.Entries where UserID == userID, or -1.
func FindUserIndex(m NetworkManifest, userID int) int {
	n := len(m.Entries)
	i := sort.Search(n, func(i int) bool { return m.Entries[i].UserID >= userID })
	if i < n && m.Entries[i].UserID == userID {
		return i
	}
	return -1
}

func insertSorted(entries []UserNetworkEntry, e UserNetworkEntry) []UserNetworkEntry {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].UserID >= e.UserID })
	entries = append(entries, UserNetworkEntry{})
	copy(entries[i+1:], entries[i:])
	entries[i] = e
	return entries
}

// GetUserFiles returns the in-memory Files for userID, or nil if not present.
// Only populated after DecryptUserFiles has been called for that entry.
func GetUserFiles(m NetworkManifest, userID int) []NetworkFileEntry {
	i := FindUserIndex(m, userID)
	if i == -1 {
		return nil
	}
	return m.Entries[i].Files
}

// UserExistsInNetwork reports whether userID has an entry in the manifest.
func UserExistsInNetwork(m NetworkManifest, userID int) bool {
	return FindUserIndex(m, userID) != -1
}

// ──────────────────────────────────────────────────────────
// Pure mutation functions (operate on in-memory Files)
// ──────────────────────────────────────────────────────────

// AddFileToNetwork adds or replaces a NetworkFileEntry for the given user.
// Requires DecryptUserFiles to have been called first for this user's entry.
func AddFileToNetwork(m NetworkManifest, userID int, username string, entry NetworkFileEntry) NetworkManifest {
	i := FindUserIndex(m, userID)
	if i == -1 {
		m.Entries = insertSorted(m.Entries, UserNetworkEntry{
			UserID:   userID,
			Username: username,
			Files:    []NetworkFileEntry{entry},
		})
		return m
	}
	for j, f := range m.Entries[i].Files {
		if f.Name == entry.Name {
			m.Entries[i].Files[j] = entry
			return m
		}
	}
	m.Entries[i].Files = append(m.Entries[i].Files, entry)
	return m
}

// RemoveFileFromNetwork removes the named file from userID's entry.
// Requires DecryptUserFiles to have been called first.
func RemoveFileFromNetwork(m NetworkManifest, userID int, filename string) NetworkManifest {
	i := FindUserIndex(m, userID)
	if i == -1 {
		return m
	}
	files := m.Entries[i].Files
	for j, f := range files {
		if f.Name == filename {
			m.Entries[i].Files = append(files[:j], files[j+1:]...)
			break
		}
	}
	if len(m.Entries[i].Files) == 0 {
		m.Entries = append(m.Entries[:i], m.Entries[i+1:]...)
	}
	return m
}

// RenameFileInNetwork renames a file within userID's entry.
// Requires DecryptUserFiles to have been called first.
func RenameFileInNetwork(m NetworkManifest, userID int, oldName, newName string) NetworkManifest {
	i := FindUserIndex(m, userID)
	if i == -1 {
		return m
	}
	for j, f := range m.Entries[i].Files {
		if f.Name == oldName {
			m.Entries[i].Files[j].Name = newName
			return m
		}
	}
	return m
}

// ──────────────────────────────────────────────────────────
// Per-user ECIES encryption + ECDSA signing
// ──────────────────────────────────────────────────────────

// ciphertextPayloadHash returns SHA-256(EphemeralPubKey || EncryptedFiles).
// This is the canonical bytes over which the ECDSA signature is computed.
// Signing the ciphertext means any peer can verify integrity without decrypting.
func ciphertextPayloadHash(entry UserNetworkEntry) []byte {
	h := sha256.New()
	h.Write(entry.EphemeralPubKey)
	h.Write(entry.EncryptedFiles)
	return h.Sum(nil)
}

// EncryptAndSignUserEntry encrypts entry.Files with ECIES (using the user's
// own public key as recipient) and signs the resulting ciphertext.
// After this call: EphemeralPubKey, EncryptedFiles, Signature, and PublicKey
// are all populated. Files is unchanged in memory but will not be serialized.
func EncryptAndSignUserEntry(entry *UserNetworkEntry, kp UserKeyPair) error {
	// Serialize the plaintext file list.
	plain, err := json.Marshal(entry.Files)
	if err != nil {
		return fmt.Errorf("could not marshal files: %w", err)
	}

	ephPub, ciphertext, err := eciesEncrypt(kp.Public, plain)
	if err != nil {
		return fmt.Errorf("could not encrypt files: %w", err)
	}
	entry.EphemeralPubKey = ephPub
	entry.EncryptedFiles = ciphertext
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	// Sign SHA-256(EphemeralPubKey || EncryptedFiles).
	hash := ciphertextPayloadHash(*entry)
	r, s, err := ecdsa.Sign(rand.Reader, kp.Private, hash)
	if err != nil {
		return fmt.Errorf("could not sign entry: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	entry.Signature = sig

	pubBytes, err := PublicKeyBytes(kp.Public)
	if err != nil {
		return fmt.Errorf("could not serialize public key: %w", err)
	}
	entry.PublicKey = pubBytes
	return nil
}

// DecryptUserFiles decrypts entry.EncryptedFiles using the owner's private key
// and populates entry.Files. Returns an error if decryption or parsing fails.
func DecryptUserFiles(entry *UserNetworkEntry, priv *ecdsa.PrivateKey) error {
	if len(entry.EncryptedFiles) == 0 {
		entry.Files = []NetworkFileEntry{}
		return nil
	}
	plain, err := eciesDecrypt(priv, entry.EphemeralPubKey, entry.EncryptedFiles)
	if err != nil {
		return fmt.Errorf("could not decrypt files: %w", err)
	}
	var files []NetworkFileEntry
	if err := json.Unmarshal(plain, &files); err != nil {
		return fmt.Errorf("could not parse decrypted files: %w", err)
	}
	entry.Files = files
	return nil
}

// VerifyUserEntry verifies the ECDSA signature on entry.
// Any peer can call this — no private key required.
// Returns false if the signature is missing, malformed, or invalid.
func VerifyUserEntry(entry UserNetworkEntry) bool {
	if len(entry.Signature) != 64 || len(entry.PublicKey) == 0 {
		return false
	}
	if len(entry.EncryptedFiles) == 0 || len(entry.EphemeralPubKey) == 0 {
		return false
	}

	pub, err := ParsePublicKeyBytes(entry.PublicKey)
	if err != nil {
		return false
	}

	hash := ciphertextPayloadHash(entry)
	r := new(big.Int).SetBytes(entry.Signature[:32])
	s := new(big.Int).SetBytes(entry.Signature[32:])
	return ecdsa.Verify(pub, hash, r, s)
}

// ──────────────────────────────────────────────────────────
// Merge (P2P sync)
// ──────────────────────────────────────────────────────────

// MergeNetworkManifest merges a remote manifest received from a peer into
// the local one. Every remote entry is signature-verified before being
// accepted. Tampered or unsigned entries are silently dropped.
//
// Merge strategy per user entry (independent of global manifest timestamp):
//   - Remote entry fails signature verification → drop, keep local
//   - Remote entry not in local manifest → add it
//   - Both exist → keep whichever has the newer per-entry UpdatedAt
//
// Returns the merged manifest and whether anything actually changed.
func MergeNetworkManifest(local, remote NetworkManifest) (NetworkManifest, bool) {
	merged := local
	changed := false

	for _, remoteEntry := range remote.Entries {
		if !VerifyUserEntry(remoteEntry) {
			fmt.Printf("MergeNetworkManifest: dropping tampered/unverified entry for userID %d\n", remoteEntry.UserID)
			continue
		}

		i := FindUserIndex(merged, remoteEntry.UserID)
		if i == -1 {
			merged.Entries = insertSorted(merged.Entries, remoteEntry)
			changed = true
			continue
		}

		// Both exist: compare per-entry timestamps and take the newer one.
		localEntryTime, _ := time.Parse(time.RFC3339, merged.Entries[i].UpdatedAt)
		remoteEntryTime, _ := time.Parse(time.RFC3339, remoteEntry.UpdatedAt)
		if remoteEntryTime.After(localEntryTime) {
			merged.Entries[i] = remoteEntry
			changed = true
		}
	}

	return merged, changed
}

// ManifestToJSON serializes the manifest to JSON bytes for P2P transmission.
// Each user's Files is excluded (json:"-"); only opaque EncryptedFiles is sent.
// This is intentional — peers receive encrypted sections they cannot read.
func ManifestToJSON(m NetworkManifest) ([]byte, error) {
	return json.Marshal(m)
}

// ManifestFromJSON deserializes a manifest received from a peer.
func ManifestFromJSON(data []byte) (NetworkManifest, error) {
	var m NetworkManifest
	err := json.Unmarshal(data, &m)
	return m, err
}

// ──────────────────────────────────────────────────────────
// ECIES (ECDH + AES-256-GCM)
// ──────────────────────────────────────────────────────────

// eciesEncrypt encrypts plaintext for the given ECDSA P-256 recipient public key.
// Uses ECDH to derive a per-message AES-256 key (SHA-256 of the shared secret).
// Returns (serialized ephemeral public key, AES-GCM ciphertext).
func eciesEncrypt(recipientPub *ecdsa.PublicKey, plaintext []byte) (ephPubBytes []byte, ciphertext []byte, err error) {
	// Generate ephemeral keypair.
	ephPriv, err := ecdsa.GenerateKey(recipientPub.Curve, rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: generate ephemeral key: %w", err)
	}

	// ECDH: convert both keys to crypto/ecdh, derive shared secret.
	ephECDH, err := ephPriv.ECDH()
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: convert ephemeral key: %w", err)
	}
	recipientECDH, err := recipientPub.ECDH()
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: convert recipient key: %w", err)
	}
	sharedSecret, err := ephECDH.ECDH(recipientECDH)
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: ECDH: %w", err)
	}

	// KDF: SHA-256 of the shared secret bytes.
	aesKey := sha256.Sum256(sharedSecret)

	// Encrypt with AES-256-GCM.
	ct, err := encryptAESGCM(aesKey, plaintext)
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: encrypt: %w", err)
	}

	// Serialize the ephemeral public key.
	ephPubBytes, err = PublicKeyBytes(&ephPriv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ecies: serialize ephemeral pub key: %w", err)
	}

	return ephPubBytes, ct, nil
}

// eciesDecrypt decrypts ciphertext produced by eciesEncrypt using the
// recipient's ECDSA private key and the sender's ephemeral public key bytes.
func eciesDecrypt(recipientPriv *ecdsa.PrivateKey, ephPubBytes []byte, ciphertext []byte) ([]byte, error) {
	// Parse the ephemeral public key.
	ephPub, err := ParsePublicKeyBytes(ephPubBytes)
	if err != nil {
		return nil, fmt.Errorf("ecies: parse ephemeral pub key: %w", err)
	}

	// ECDH: convert to crypto/ecdh keys.
	recipientECDH, err := recipientPriv.ECDH()
	if err != nil {
		return nil, fmt.Errorf("ecies: convert recipient priv key: %w", err)
	}
	ephECDH, err := ephPub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("ecies: convert ephemeral pub key: %w", err)
	}
	sharedSecret, err := recipientECDH.ECDH(ephECDH)
	if err != nil {
		return nil, fmt.Errorf("ecies: ECDH: %w", err)
	}

	// KDF: SHA-256 of shared secret.
	aesKey := sha256.Sum256(sharedSecret)

	return decryptAESGCM(aesKey, ciphertext)
}

// ──────────────────────────────────────────────────────────
// AES-256-GCM primitives (at-rest encryption layer)
// ──────────────────────────────────────────────────────────

// encryptAESGCM encrypts plaintext with AES-256-GCM.
// Output: [12-byte random nonce] || [GCM ciphertext+tag].
func encryptAESGCM(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptAESGCM decrypts data produced by encryptAESGCM.
func decryptAESGCM(key [32]byte, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
