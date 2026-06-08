package fileSystem

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
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

// blockMeta holds the private per-record metadata encrypted into EncryptedMeta.
type blockMeta struct {
	Name      string `json:"n"`
	Size      int    `json:"s,omitempty"`
	DateAdded string `json:"d,omitempty"`
}

// MetaKeyFromKP derives the AES-256 key used to encrypt record metadata from the
// owner's ECDSA private key. Only the owner can decrypt their own records.
func MetaKeyFromKP(kp UserKeyPair) [32]byte {
	h := sha256.New()
	h.Write(kp.Private.D.Bytes())
	h.Write([]byte("mosaic-block-meta"))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func encryptBlockMeta(m blockMeta, key [32]byte) ([]byte, error) {
	plain, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
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
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decryptBlockMeta(data []byte, key [32]byte) (blockMeta, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return blockMeta{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return blockMeta{}, err
	}
	if len(data) < gcm.NonceSize() {
		return blockMeta{}, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return blockMeta{}, err
	}
	var m blockMeta
	return m, json.Unmarshal(plain, &m)
}

// ──────────────────────────────────────────────────────────
// LWW-Set CRDT types
// ──────────────────────────────────────────────────────────

// FileRecord is a signed LWW entry for one file owned by one user.
// The entry with the highest Seq for a given ContentHash is canonical.
//
// Security model:
//   - Each record is ECDSA-signed. Any peer can verify the signature using
//     the PublicKey embedded in the parent UserState.
//   - Seq is monotonically increasing per (userID, contentHash), preventing
//     replay of older state: a peer rejects any incoming record whose Seq is
//     equal to or lower than the locally stored one.
//   - Tampering with any field (ContentHash, Seq, Deleted, EncryptedMeta)
//     invalidates the signature, so peers can detect and drop forged records.
//   - File metadata (name, size, date) is AES-256-GCM encrypted; non-owners
//     see only ContentHash.
type FileRecord struct {
	ContentHash   string `json:"contentHash"`
	EncryptedMeta []byte `json:"encryptedMeta,omitempty"` // AES-GCM: {name, size, dateAdded}
	Seq           uint64 `json:"seq"`                     // monotonically increasing per contentHash
	Deleted       bool   `json:"deleted"`                 // tombstone for removes
	Timestamp     string `json:"timestamp"`
	Signature     []byte `json:"signature"` // ECDSA over canonical payload
}

// UserState is the per-user LWW-set: one FileRecord per ContentHash.
type UserState struct {
	UserID    int                    `json:"userID"`
	Username  string                 `json:"username"`
	PublicKey []byte                 `json:"publicKey"` // PKIX DER P-256
	Records   map[string]*FileRecord `json:"records"`   // contentHash → FileRecord
}

// ShardLocations records which peers hold each shard for a given file.
// This is a G-set per shard index: peer node IDs are only ever added, never removed.
// Keyed by shard index (0-based); values are deduplicated node ID strings.
type ShardLocations struct {
	Holders map[int][]string `json:"holders"` // shardIndex → []nodeID
}

// NetworkManifest is the root structure: a collection of per-user LWW-sets,
// encrypted at rest with the shared network AES key.
// ShardMap tracks shard-to-peer assignments and is merged as a G-set.
type NetworkManifest struct {
	Version   int                        `json:"version"`
	UpdatedAt string                     `json:"updatedAt"`
	Users     map[int]*UserState         `json:"users"`
	ShardMap  map[string]*ShardLocations `json:"shardMap,omitempty"`
}

// networkManifestPath returns the path to the on-disk manifest file.
func networkManifestPath(mosaicDir string) string {
	return filepath.Join(mosaicDir, networkManifestFilename)
}

// ──────────────────────────────────────────────────────────
// Record signing and verification
// ──────────────────────────────────────────────────────────

// recordHashBytes computes SHA-256 over the canonical serialization of a FileRecord.
// The payload commits to userID, contentHash, seq, deleted flag, and encryptedMeta
// so that any tampering with these fields invalidates the signature.
func recordHashBytes(userID int, r *FileRecord) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(userID)) //nolint:errcheck — bytes.Buffer never fails
	buf.WriteString(r.ContentHash)
	binary.Write(&buf, binary.BigEndian, r.Seq) //nolint:errcheck
	if r.Deleted {
		buf.WriteByte(0x01)
	} else {
		buf.WriteByte(0x00)
	}
	buf.Write(r.EncryptedMeta)
	h := sha256.Sum256(buf.Bytes())
	return h[:]
}

func signRecord(userID int, r *FileRecord, priv *ecdsa.PrivateKey) error {
	hash := recordHashBytes(userID, r)
	rr, s, err := ecdsa.Sign(rand.Reader, priv, hash)
	if err != nil {
		return fmt.Errorf("signRecord: ECDSA sign: %w", err)
	}
	sig := make([]byte, 64)
	rr.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	r.Signature = sig
	return nil
}

func verifyRecord(userID int, r *FileRecord, pub *ecdsa.PublicKey) bool {
	if len(r.Signature) != 64 {
		return false
	}
	hash := recordHashBytes(userID, r)
	rr := new(big.Int).SetBytes(r.Signature[:32])
	s := new(big.Int).SetBytes(r.Signature[32:])
	return ecdsa.Verify(pub, hash, rr, s)
}

// ──────────────────────────────────────────────────────────
// LWW-Set mutations
// ──────────────────────────────────────────────────────────

// ensureUserState returns the UserState for userID, creating it if absent.
func ensureUserState(m *NetworkManifest, userID int, username string, kp UserKeyPair) *UserState {
	if m.Users == nil {
		m.Users = make(map[int]*UserState)
	}
	us, ok := m.Users[userID]
	if !ok || us == nil {
		pubBytes, _ := PublicKeyBytes(kp.Public)
		us = &UserState{
			UserID:    userID,
			Username:  username,
			PublicKey: pubBytes,
			Records:   make(map[string]*FileRecord),
		}
		m.Users[userID] = us
	}
	return us
}

// RecordFileAdd records a file-add operation for userID in the manifest.
// Creates a new FileRecord with encrypted metadata and an ECDSA signature.
func RecordFileAdd(m *NetworkManifest, userID int, username string, file NetworkFileEntry, kp UserKeyPair) error {
	us := ensureUserState(m, userID, username, kp)

	var nextSeq uint64 = 1
	if existing := us.Records[file.ContentHash]; existing != nil {
		nextSeq = existing.Seq + 1
	}

	metaKey := MetaKeyFromKP(kp)
	encMeta, err := encryptBlockMeta(blockMeta{
		Name:      file.Name,
		Size:      file.Size,
		DateAdded: file.DateAdded,
	}, metaKey)
	if err != nil {
		return fmt.Errorf("RecordFileAdd: encrypt metadata: %w", err)
	}

	r := &FileRecord{
		ContentHash:   file.ContentHash,
		EncryptedMeta: encMeta,
		Seq:           nextSeq,
		Deleted:       false,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := signRecord(userID, r, kp.Private); err != nil {
		return err
	}
	us.Records[file.ContentHash] = r
	return nil
}

// RecordFileRemove records a file-remove (tombstone) for contentHash in userID's state.
func RecordFileRemove(m *NetworkManifest, userID int, contentHash string, kp UserKeyPair) error {
	us := ensureUserState(m, userID, "", kp)

	var nextSeq uint64 = 1
	if existing := us.Records[contentHash]; existing != nil {
		nextSeq = existing.Seq + 1
	}

	r := &FileRecord{
		ContentHash: contentHash,
		Seq:         nextSeq,
		Deleted:     true,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := signRecord(userID, r, kp.Private); err != nil {
		return err
	}
	us.Records[contentHash] = r
	return nil
}

// RecordFileRename updates the display name for contentHash in userID's state.
// The contentHash is stable (file bytes don't change), only the name differs.
func RecordFileRename(m *NetworkManifest, userID int, contentHash, newName string, kp UserKeyPair) error {
	us := ensureUserState(m, userID, "", kp)

	existing := us.Records[contentHash]
	if existing == nil || existing.Deleted {
		return fmt.Errorf("RecordFileRename: file not found for user %d", userID)
	}

	metaKey := MetaKeyFromKP(kp)
	existingMeta, err := decryptBlockMeta(existing.EncryptedMeta, metaKey)
	if err != nil {
		return fmt.Errorf("RecordFileRename: decrypt existing meta: %w", err)
	}

	encMeta, err := encryptBlockMeta(blockMeta{
		Name:      newName,
		Size:      existingMeta.Size,
		DateAdded: existingMeta.DateAdded,
	}, metaKey)
	if err != nil {
		return fmt.Errorf("RecordFileRename: encrypt new meta: %w", err)
	}

	r := &FileRecord{
		ContentHash:   contentHash,
		EncryptedMeta: encMeta,
		Seq:           existing.Seq + 1,
		Deleted:       false,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := signRecord(userID, r, kp.Private); err != nil {
		return err
	}
	us.Records[contentHash] = r
	return nil
}

// ──────────────────────────────────────────────────────────
// Manifest-level queries
// ──────────────────────────────────────────────────────────

// GetUserFiles returns all non-deleted files for userID.
// Pass a non-nil metaKey (from MetaKeyFromKP) to decrypt names and full metadata;
// pass nil for non-owner access — only ContentHash is returned in that case.
func GetUserFiles(m NetworkManifest, userID int, metaKey *[32]byte) []NetworkFileEntry {
	us := m.Users[userID]
	if us == nil {
		return nil
	}
	var result []NetworkFileEntry
	for _, r := range us.Records {
		if r == nil || r.Deleted {
			continue
		}
		entry := NetworkFileEntry{ContentHash: r.ContentHash, PrimaryNodeID: userID}
		if metaKey != nil && len(r.EncryptedMeta) > 0 {
			if meta, err := decryptBlockMeta(r.EncryptedMeta, *metaKey); err == nil {
				entry.Name = meta.Name
				entry.Size = meta.Size
				entry.DateAdded = meta.DateAdded
			}
		}
		result = append(result, entry)
	}
	return result
}

// AllNetworkFiles returns all non-deleted NetworkFileEntries across all users
// without decrypting metadata (non-owner path: only ContentHash is set).
func AllNetworkFiles(m NetworkManifest) []NetworkFileEntry {
	var result []NetworkFileEntry
	for _, us := range m.Users {
		if us == nil {
			continue
		}
		for _, r := range us.Records {
			if r == nil || r.Deleted {
				continue
			}
			result = append(result, NetworkFileEntry{ContentHash: r.ContentHash})
		}
	}
	return result
}

// FindFileByName looks up a file's ContentHash by filename for the given user.
// Returns empty string and false if not found.
func FindFileByName(m NetworkManifest, userID int, filename string, metaKey *[32]byte) (string, bool) {
	for _, f := range GetUserFiles(m, userID, metaKey) {
		if f.Name == filename {
			return f.ContentHash, true
		}
	}
	return "", false
}

// UserExistsInNetwork reports whether userID has a state entry in the manifest.
func UserExistsInNetwork(m NetworkManifest, userID int) bool {
	return m.Users[userID] != nil
}

// ──────────────────────────────────────────────────────────
// Disk I/O (outer AES-256-GCM at-rest encryption)
// ──────────────────────────────────────────────────────────

// EnsureNetworkManifest writes an empty encrypted v3 manifest to disk if one
// does not already exist. Call at daemon startup so that concurrent
// ManifestSync merges always find a valid file rather than racing on first write.
func EnsureNetworkManifest(mosaicDir string, key [32]byte) error {
	p := networkManifestPath(mosaicDir)
	if _, err := os.Stat(p); err == nil {
		return nil // already exists
	}
	empty := NetworkManifest{Version: 3, Users: make(map[int]*UserState), ShardMap: make(map[string]*ShardLocations)}
	return WriteNetworkManifest(mosaicDir, key, empty)
}

// ReadNetworkManifest decrypts and deserializes the manifest from disk.
// Returns an empty v3 manifest if the file does not exist.
// Manifests with version < 3 are treated as empty (no migration).
func ReadNetworkManifest(mosaicDir string, key [32]byte) (NetworkManifest, error) {
	empty := NetworkManifest{Version: 3, Users: make(map[int]*UserState), ShardMap: make(map[string]*ShardLocations)}

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

	if m.Version < 3 {
		return empty, nil
	}
	if m.Users == nil {
		m.Users = make(map[int]*UserState)
	}
	if m.ShardMap == nil {
		m.ShardMap = make(map[string]*ShardLocations)
	}
	return m, nil
}

// WriteNetworkManifest serializes, encrypts, and atomically writes the manifest.
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

// WriteNetworkManifestLocked is WriteNetworkManifest with the shared mutex held.
// Use this for any read-modify-write cycle that must be atomic.
func WriteNetworkManifestLocked(mosaicDir string, key [32]byte, m NetworkManifest) error {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()
	return WriteNetworkManifest(mosaicDir, key, m)
}

// RecordShardHolderAndWrite atomically reads the local manifest, records nodeID
// as a holder of shardIndex for contentHash, and writes the result — all under
// one mutex acquisition so concurrent shard callbacks cannot lose each other's
// writes. Returns the updated manifest and true if the manifest was modified.
func RecordShardHolderAndWrite(mosaicDir string, key [32]byte, contentHash string, shardIndex int, nodeID string) (NetworkManifest, bool, error) {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	m, err := ReadNetworkManifest(mosaicDir, key)
	if err != nil {
		m = NetworkManifest{Version: 3, Users: make(map[int]*UserState), ShardMap: make(map[string]*ShardLocations)}
	}
	if !RecordShardHolder(&m, contentHash, shardIndex, nodeID) {
		return m, false, nil
	}
	return m, true, WriteNetworkManifest(mosaicDir, key, m)
}

// MergeAndWriteNetworkManifest reads the local manifest, merges remote into it,
// and writes the result — all under one mutex acquisition so concurrent
// handleManifestSync goroutines cannot interleave their read-modify-write cycles.
// Returns the merged manifest and whether it differed from the local copy.
func MergeAndWriteNetworkManifest(mosaicDir string, key [32]byte, remote NetworkManifest) (NetworkManifest, bool, error) {
	networkManifestMu.Lock()
	defer networkManifestMu.Unlock()

	local, err := ReadNetworkManifest(mosaicDir, key)
	if err != nil {
		fmt.Printf("MergeAndWriteNetworkManifest: local manifest unreadable (%v) — recovering from remote\n", err)
		local = NetworkManifest{Version: 3, Users: make(map[int]*UserState), ShardMap: make(map[string]*ShardLocations)}
	}

	merged, changed := MergeNetworkManifest(local, remote)
	if !changed {
		return merged, false, nil
	}

	return merged, true, WriteNetworkManifest(mosaicDir, key, merged)
}

// ──────────────────────────────────────────────────────────
// Merge (P2P sync)
// ──────────────────────────────────────────────────────────

// MergeNetworkManifest merges a remote manifest into the local one using LWW semantics.
//
// Per-user merge strategy:
//   - New user in remote → accept all records that pass signature verification
//   - Existing user → for each (contentHash): higher Seq wins; equal Seq is idempotent
//   - Records with invalid signatures are silently dropped
//
// Returns the merged manifest and whether anything actually changed.
func MergeNetworkManifest(local, remote NetworkManifest) (NetworkManifest, bool) {
	merged := local
	if merged.Users == nil {
		merged.Users = make(map[int]*UserState)
	}
	changed := false

	for userID, remoteUser := range remote.Users {
		if remoteUser == nil {
			continue
		}
		pub, err := ParsePublicKeyBytes(remoteUser.PublicKey)
		if err != nil {
			fmt.Printf("MergeNetworkManifest: dropping user %d: bad public key\n", userID)
			continue
		}

		localUser := merged.Users[userID]
		if localUser == nil {
			localUser = &UserState{
				UserID:    remoteUser.UserID,
				Username:  remoteUser.Username,
				PublicKey: remoteUser.PublicKey,
				Records:   make(map[string]*FileRecord),
			}
			merged.Users[userID] = localUser
		}

		for contentHash, remoteRecord := range remoteUser.Records {
			if remoteRecord == nil {
				continue
			}
			if !verifyRecord(userID, remoteRecord, pub) {
				fmt.Printf("MergeNetworkManifest: dropping invalid record %s for user %d\n", hex.EncodeToString([]byte(contentHash))[:min8(len(contentHash)*2)], userID)
				continue
			}
			localRecord := localUser.Records[contentHash]
			if localRecord == nil || remoteRecord.Seq > localRecord.Seq {
				localUser.Records[contentHash] = remoteRecord
				changed = true
			}
		}
	}

	if mergedMap, shardChanged := mergeShardMaps(merged.ShardMap, remote.ShardMap); shardChanged {
		merged.ShardMap = mergedMap
		changed = true
	}

	return merged, changed
}

// ──────────────────────────────────────────────────────────
// Shard location tracking (G-set per shard index)
// ──────────────────────────────────────────────────────────

// RecordShardHolder adds nodeID to the holder set for shardIndex of the file
// identified by contentHash. Returns true if the manifest was modified (i.e.
// nodeID was not already recorded for that shard).
func RecordShardHolder(m *NetworkManifest, contentHash string, shardIndex int, nodeID string) bool {
	if m.ShardMap == nil {
		m.ShardMap = make(map[string]*ShardLocations)
	}
	loc, ok := m.ShardMap[contentHash]
	if !ok {
		loc = &ShardLocations{Holders: make(map[int][]string)}
		m.ShardMap[contentHash] = loc
	}
	for _, id := range loc.Holders[shardIndex] {
		if id == nodeID {
			return false // already recorded
		}
	}
	loc.Holders[shardIndex] = append(loc.Holders[shardIndex], nodeID)
	return true
}

// RemoveShardHolder removes nodeID from every shard holder list in the manifest.
// Called when a peer is evicted so GetShardHolders stops routing to dead nodes.
// Returns true if any entry was removed.
func RemoveShardHolder(m *NetworkManifest, nodeID string) bool {
	if m.ShardMap == nil {
		return false
	}
	changed := false
	for _, loc := range m.ShardMap {
		for shardIdx, holders := range loc.Holders {
			filtered := holders[:0]
			for _, id := range holders {
				if id != nodeID {
					filtered = append(filtered, id)
				} else {
					changed = true
				}
			}
			loc.Holders[shardIdx] = filtered
		}
	}
	return changed
}

// GetShardHolders returns the list of node IDs that hold shardIndex for the
// file with contentHash. Returns nil if unknown.
func GetShardHolders(m NetworkManifest, contentHash string, shardIndex int) []string {
	if m.ShardMap == nil {
		return nil
	}
	loc, ok := m.ShardMap[contentHash]
	if !ok {
		return nil
	}
	return loc.Holders[shardIndex]
}

// mergeShardMaps unions the remote shard map into local. Returns true if any
// new holder was added.
func mergeShardMaps(local, remote map[string]*ShardLocations) (map[string]*ShardLocations, bool) {
	changed := false
	if local == nil {
		local = make(map[string]*ShardLocations)
	}
	for hash, remoteLoc := range remote {
		localLoc, ok := local[hash]
		if !ok {
			cp := &ShardLocations{Holders: make(map[int][]string)}
			for idx, ids := range remoteLoc.Holders {
				cp.Holders[idx] = append([]string(nil), ids...)
			}
			local[hash] = cp
			changed = true
			continue
		}
		for idx, remoteIDs := range remoteLoc.Holders {
			existing := make(map[string]bool, len(localLoc.Holders[idx]))
			for _, id := range localLoc.Holders[idx] {
				existing[id] = true
			}
			for _, id := range remoteIDs {
				if !existing[id] {
					localLoc.Holders[idx] = append(localLoc.Holders[idx], id)
					changed = true
				}
			}
		}
	}
	return local, changed
}

// ManifestToJSON serializes the manifest to JSON bytes for P2P transmission.
func ManifestToJSON(m NetworkManifest) ([]byte, error) {
	return json.Marshal(m)
}

// ManifestFromJSON deserializes a manifest received from a peer.
func ManifestFromJSON(data []byte) (NetworkManifest, error) {
	var m NetworkManifest
	err := json.Unmarshal(data, &m)
	if m.Users == nil {
		m.Users = make(map[int]*UserState)
	}
	if m.ShardMap == nil {
		m.ShardMap = make(map[string]*ShardLocations)
	}
	return m, err
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

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
