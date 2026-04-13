package fileSystem

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const networkManifestFilename = ".mosaic-network-manifest"

// networkManifestMu guards all read-modify-write cycles on the network manifest.
var networkManifestMu sync.Mutex

// NetworkFileEntry is the per-file record stored in the network manifest.
type NetworkFileEntry struct {
	Name          string `json:"name"`
	Size          int    `json:"size"`
	PrimaryNodeID int    `json:"primaryNodeID"`
	DateAdded     string `json:"dateAdded"`
	ContentHash   string `json:"contentHash"` // SHA-256 hex; populated once hashing is wired into upload
}

// UserNetworkEntry groups all files owned by one user.
// The parent NetworkManifest.Entries slice is kept sorted by UserID.
type UserNetworkEntry struct {
	UserID   int                `json:"userID"`
	Username string             `json:"username"`
	Files    []NetworkFileEntry `json:"files"`
}

// NetworkManifest is the root structure written to disk after encryption.
// Entries MUST remain sorted by UserID at all times.
type NetworkManifest struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updatedAt"`
	Entries   []UserNetworkEntry `json:"entries"`
}

// networkManifestPath returns the path to the encrypted network manifest file.
func networkManifestPath(mosaicDir string) string {
	return filepath.Join(mosaicDir, networkManifestFilename)
}

// ReadNetworkManifest decrypts and deserializes the network manifest from disk.
// Returns an empty manifest (Version=1, Entries=[]) if the file does not exist.
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

// WriteNetworkManifest serializes, encrypts, and atomically writes the manifest to disk.
// It sets UpdatedAt to the current time before writing.
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

// findUserIndex returns the index in m.Entries where UserID == userID,
// or -1 if not found. Uses sort.Search (binary search).
func findUserIndex(m NetworkManifest, userID int) int {
	n := len(m.Entries)
	i := sort.Search(n, func(i int) bool {
		return m.Entries[i].UserID >= userID
	})
	if i < n && m.Entries[i].UserID == userID {
		return i
	}
	return -1
}

// insertSorted inserts a new UserNetworkEntry in the correct sorted position.
func insertSorted(entries []UserNetworkEntry, e UserNetworkEntry) []UserNetworkEntry {
	i := sort.Search(len(entries), func(i int) bool {
		return entries[i].UserID >= e.UserID
	})
	entries = append(entries, UserNetworkEntry{})
	copy(entries[i+1:], entries[i:])
	entries[i] = e
	return entries
}

// GetUserFiles returns the file list for userID, or nil if no entry exists.
func GetUserFiles(m NetworkManifest, userID int) []NetworkFileEntry {
	i := findUserIndex(m, userID)
	if i == -1 {
		return nil
	}
	return m.Entries[i].Files
}

// UserExistsInNetwork reports whether userID has any files in the manifest.
func UserExistsInNetwork(m NetworkManifest, userID int) bool {
	return findUserIndex(m, userID) != -1
}

// AddFileToNetwork adds or replaces a NetworkFileEntry for the given user.
// Inserts a new UserNetworkEntry if the user is not yet present, maintaining sort order.
// Returns the updated manifest (does NOT write to disk — caller calls WriteNetworkManifest).
func AddFileToNetwork(m NetworkManifest, userID int, username string, entry NetworkFileEntry) NetworkManifest {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	i := findUserIndex(m, userID)
	if i == -1 {
		m.Entries = insertSorted(m.Entries, UserNetworkEntry{
			UserID:   userID,
			Username: username,
			Files:    []NetworkFileEntry{entry},
		})
		return m
	}

	// Replace existing file entry if name matches, otherwise append.
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
// If the user has no remaining files, the UserNetworkEntry is removed entirely.
// Returns the updated manifest (does NOT write to disk).
func RemoveFileFromNetwork(m NetworkManifest, userID int, filename string) NetworkManifest {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	i := findUserIndex(m, userID)
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
// Returns the updated manifest (does NOT write to disk).
func RenameFileInNetwork(m NetworkManifest, userID int, oldName, newName string) NetworkManifest {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	i := findUserIndex(m, userID)
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

// MergeNetworkManifest merges a received manifest with the local one.
// Accepts the newer manifest by UpdatedAt timestamp.
// On a tie, accepts the one with more total files (more information wins).
func MergeNetworkManifest(local, remote NetworkManifest) NetworkManifest {
	localTime, _ := time.Parse(time.RFC3339, local.UpdatedAt)
	remoteTime, _ := time.Parse(time.RFC3339, remote.UpdatedAt)

	if remoteTime.After(localTime) {
		return remote
	}
	if localTime.After(remoteTime) {
		return local
	}

	// Tie: count total files.
	localCount, remoteCount := 0, 0
	for _, u := range local.Entries {
		localCount += len(u.Files)
	}
	for _, u := range remote.Entries {
		remoteCount += len(u.Files)
	}
	if remoteCount > localCount {
		return remote
	}
	return local
}

// encryptAESGCM encrypts plaintext using AES-256-GCM with a random nonce.
// Output format: [12-byte nonce] || [GCM ciphertext+tag].
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

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
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

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
