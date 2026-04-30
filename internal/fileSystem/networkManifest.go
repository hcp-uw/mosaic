package fileSystem

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// Block operation types.
const (
	BlockOpAdd    = "add"
	BlockOpRemove = "remove"
	BlockOpRename = "rename"
)

// ChainBlock is one signed, hash-linked operation in a user's file history.
// Blocks are append-only and never modified after creation.
//
// Security model:
//   - Each block is ECDSA-signed by the owner.  Any peer can verify the signature
//     using the PublicKey embedded in the parent UserChain.
//   - PrevHash links each block to its predecessor, making the chain tamper-evident:
//     altering any past block invalidates every subsequent hash link.
//   - Merge conflict resolution: the longer valid chain wins.  Equal-length forks
//     resolve deterministically by choosing the chain with the lower hex hash at
//     the first differing block.
type ChainBlock struct {
	Index     int              `json:"index"`             // 0-based position in chain
	PrevHash  string           `json:"prevHash"`          // hex SHA-256 of previous block; "" for genesis
	Op        string           `json:"op"`                // BlockOpAdd | BlockOpRemove | BlockOpRename
	File      NetworkFileEntry `json:"file"`              // file affected by this operation
	NewName   string           `json:"newName,omitempty"` // populated only for BlockOpRename
	Timestamp string           `json:"timestamp"`         // RFC3339 UTC
	Signature []byte           `json:"signature,omitempty"`
}

// UserChain is a user's append-only operation history.
// The current file set is derived by replaying Blocks via ChainToFiles.
type UserChain struct {
	UserID    int          `json:"userID"`
	Username  string       `json:"username"`
	PublicKey []byte       `json:"publicKey"` // PKIX DER P-256; used for block verification

	Blocks []ChainBlock `json:"blocks"`

	// In-memory cache: populated by ChainToFiles; never serialized.
	Files []NetworkFileEntry `json:"-"`
}

// ShardLocations records which peers hold each shard for a given file.
// This is a G-set per shard index: peer node IDs are only ever added, never removed.
// Keyed by shard index (0-based); values are deduplicated node ID strings.
type ShardLocations struct {
	Holders map[int][]string `json:"holders"` // shardIndex → []nodeID
}

// NetworkManifest is the root structure: a collection of per-user chains,
// encrypted at rest with the shared network AES key.
// Chains MUST remain sorted by UserID at all times.
// ShardMap tracks shard-to-peer assignments and is merged as a G-set.
type NetworkManifest struct {
	Version   int                        `json:"version"`
	UpdatedAt string                     `json:"updatedAt"`
	Chains    []UserChain                `json:"chains"`
	ShardMap  map[string]*ShardLocations `json:"shardMap,omitempty"` // contentHash → shard locations
}

// networkManifestPath returns the path to the on-disk manifest file.
func networkManifestPath(mosaicDir string) string {
	return filepath.Join(mosaicDir, networkManifestFilename)
}

// ──────────────────────────────────────────────────────────
// Block hashing and signing
// ──────────────────────────────────────────────────────────

// blockHashBytes returns the SHA-256 of the block with Signature zeroed.
// This is the canonical pre-image for signing and for chain linking.
func blockHashBytes(b ChainBlock) ([]byte, error) {
	b.Signature = nil
	data, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("blockHash: marshal: %w", err)
	}
	h := sha256.Sum256(data)
	return h[:], nil
}

// BlockHash returns the hex SHA-256 of the block (Signature excluded).
func BlockHash(b ChainBlock) (string, error) {
	h, err := blockHashBytes(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h), nil
}

func signBlock(b *ChainBlock, priv *ecdsa.PrivateKey) error {
	hash, err := blockHashBytes(*b)
	if err != nil {
		return err
	}
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash)
	if err != nil {
		return fmt.Errorf("signBlock: ECDSA sign: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	b.Signature = sig
	return nil
}

func verifyBlock(b ChainBlock, pub *ecdsa.PublicKey) bool {
	if len(b.Signature) != 64 {
		return false
	}
	hash, err := blockHashBytes(b)
	if err != nil {
		return false
	}
	r := new(big.Int).SetBytes(b.Signature[:32])
	s := new(big.Int).SetBytes(b.Signature[32:])
	return ecdsa.Verify(pub, hash, r, s)
}

// ValidateChain checks every block's signature and every hash link.
// An empty chain is valid.  Returns false on any integrity failure.
func ValidateChain(chain UserChain) bool {
	if len(chain.Blocks) == 0 {
		return true
	}
	if len(chain.PublicKey) == 0 {
		return false
	}
	pub, err := ParsePublicKeyBytes(chain.PublicKey)
	if err != nil {
		return false
	}

	prevHash := ""
	for i, b := range chain.Blocks {
		if b.Index != i {
			return false
		}
		if b.PrevHash != prevHash {
			return false
		}
		if !verifyBlock(b, pub) {
			return false
		}
		h, err := BlockHash(b)
		if err != nil {
			return false
		}
		prevHash = h
	}
	return true
}

// ──────────────────────────────────────────────────────────
// Chain mutation and replay
// ──────────────────────────────────────────────────────────

// AppendBlock creates a new signed block and appends it to chain.
// kp must be the owner's keypair.
func AppendBlock(chain *UserChain, op string, file NetworkFileEntry, newName string, kp UserKeyPair) error {
	prevHash := ""
	if len(chain.Blocks) > 0 {
		last := chain.Blocks[len(chain.Blocks)-1]
		h, err := BlockHash(last)
		if err != nil {
			return fmt.Errorf("AppendBlock: hash last block: %w", err)
		}
		prevHash = h
	}

	b := ChainBlock{
		Index:     len(chain.Blocks),
		PrevHash:  prevHash,
		Op:        op,
		File:      file,
		NewName:   newName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if err := signBlock(&b, kp.Private); err != nil {
		return err
	}
	chain.Blocks = append(chain.Blocks, b)
	return nil
}

// ChainToFiles replays all blocks to compute the current set of files.
func ChainToFiles(chain UserChain) []NetworkFileEntry {
	files := make(map[string]NetworkFileEntry)
	for _, b := range chain.Blocks {
		switch b.Op {
		case BlockOpAdd:
			files[b.File.Name] = b.File
		case BlockOpRemove:
			delete(files, b.File.Name)
		case BlockOpRename:
			if f, ok := files[b.File.Name]; ok {
				delete(files, b.File.Name)
				f.Name = b.NewName
				files[b.NewName] = f
			}
		}
	}
	result := make([]NetworkFileEntry, 0, len(files))
	for _, f := range files {
		result = append(result, f)
	}
	return result
}

// ──────────────────────────────────────────────────────────
// Manifest-level helpers (sorted chain list)
// ──────────────────────────────────────────────────────────

// FindChainIndex returns the index in m.Chains where UserID == userID, or -1.
func FindChainIndex(m NetworkManifest, userID int) int {
	n := len(m.Chains)
	i := sort.Search(n, func(i int) bool { return m.Chains[i].UserID >= userID })
	if i < n && m.Chains[i].UserID == userID {
		return i
	}
	return -1
}

func insertChainSorted(chains []UserChain, c UserChain) []UserChain {
	i := sort.Search(len(chains), func(i int) bool { return chains[i].UserID >= c.UserID })
	chains = append(chains, UserChain{})
	copy(chains[i+1:], chains[i:])
	chains[i] = c
	return chains
}

// AppendBlockAdd appends an "add" block to userID's chain in the manifest.
func AppendBlockAdd(m *NetworkManifest, userID int, username string, file NetworkFileEntry, kp UserKeyPair) error {
	i := FindChainIndex(*m, userID)
	if i == -1 {
		pubBytes, err := PublicKeyBytes(kp.Public)
		if err != nil {
			return fmt.Errorf("AppendBlockAdd: serialize public key: %w", err)
		}
		m.Chains = insertChainSorted(m.Chains, UserChain{
			UserID:    userID,
			Username:  username,
			PublicKey: pubBytes,
		})
		i = FindChainIndex(*m, userID)
	}
	return AppendBlock(&m.Chains[i], BlockOpAdd, file, "", kp)
}

// AppendBlockRemove appends a "remove" block to userID's chain.
func AppendBlockRemove(m *NetworkManifest, userID int, filename string, kp UserKeyPair) error {
	i := FindChainIndex(*m, userID)
	if i == -1 {
		return fmt.Errorf("AppendBlockRemove: user %d not in manifest", userID)
	}
	file := NetworkFileEntry{Name: filename}
	return AppendBlock(&m.Chains[i], BlockOpRemove, file, "", kp)
}

// AppendBlockRename appends a "rename" block to userID's chain.
func AppendBlockRename(m *NetworkManifest, userID int, oldName, newName string, kp UserKeyPair) error {
	i := FindChainIndex(*m, userID)
	if i == -1 {
		return fmt.Errorf("AppendBlockRename: user %d not in manifest", userID)
	}
	file := NetworkFileEntry{Name: oldName}
	return AppendBlock(&m.Chains[i], BlockOpRename, file, newName, kp)
}

// GetUserFiles returns the current file list for userID by replaying the chain.
// Returns nil if the user has no chain in the manifest.
func GetUserFiles(m NetworkManifest, userID int) []NetworkFileEntry {
	i := FindChainIndex(m, userID)
	if i == -1 {
		return nil
	}
	return ChainToFiles(m.Chains[i])
}

// UserExistsInNetwork reports whether userID has a chain in the manifest.
func UserExistsInNetwork(m NetworkManifest, userID int) bool {
	return FindChainIndex(m, userID) != -1
}

// ──────────────────────────────────────────────────────────
// Disk I/O (outer AES-256-GCM at-rest encryption)
// ──────────────────────────────────────────────────────────

// ReadNetworkManifest decrypts and deserializes the manifest from disk.
// Returns an empty v2 manifest if the file does not exist.
func ReadNetworkManifest(mosaicDir string, key [32]byte) (NetworkManifest, error) {
	empty := NetworkManifest{Version: 2, Chains: []UserChain{}, ShardMap: make(map[string]*ShardLocations)}

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

	// Treat old v1 manifests as empty — they used a different schema.
	if m.Version < 2 {
		return empty, nil
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

// ──────────────────────────────────────────────────────────
// Merge (P2P sync)
// ──────────────────────────────────────────────────────────

// MergeNetworkManifest merges a remote manifest into the local one.
//
// Per-user merge strategy:
//   - Remote chain fails ValidateChain → silently dropped
//   - Remote chain not in local manifest → accepted
//   - Both exist → longer valid chain wins
//   - Same length but divergent → deterministic fork resolution: the chain
//     whose first differing block has the lexicographically lower hash wins
//
// Returns the merged manifest and whether anything actually changed.
func MergeNetworkManifest(local, remote NetworkManifest) (NetworkManifest, bool) {
	merged := local
	changed := false

	for _, remoteChain := range remote.Chains {
		if !ValidateChain(remoteChain) {
			fmt.Printf("MergeNetworkManifest: dropping invalid chain for userID=%d\n", remoteChain.UserID)
			continue
		}

		i := FindChainIndex(merged, remoteChain.UserID)
		if i == -1 {
			merged.Chains = insertChainSorted(merged.Chains, remoteChain)
			changed = true
			continue
		}

		winner := pickBetterChain(merged.Chains[i], remoteChain)
		if len(winner.Blocks) != len(merged.Chains[i].Blocks) || chainHeadHash(winner) != chainHeadHash(merged.Chains[i]) {
			merged.Chains[i] = winner
			changed = true
		}
	}

	// Merge shard location maps (G-set union).
	if mergedMap, shardChanged := mergeShardMaps(merged.ShardMap, remote.ShardMap); shardChanged {
		merged.ShardMap = mergedMap
		changed = true
	}

	return merged, changed
}

// pickBetterChain returns whichever chain should be the canonical one.
// Longer valid chain wins; equal-length forks resolve by first differing block hash.
func pickBetterChain(a, b UserChain) UserChain {
	if len(a.Blocks) > len(b.Blocks) {
		return a
	}
	if len(b.Blocks) > len(a.Blocks) {
		return b
	}
	// Same length: find first differing block and take the one with the lower hash.
	for i := range a.Blocks {
		ha, _ := BlockHash(a.Blocks[i])
		hb, _ := BlockHash(b.Blocks[i])
		if ha != hb {
			if ha < hb {
				return a
			}
			return b
		}
	}
	return a // identical chains
}

// chainHeadHash returns the hash of the last block, or "" for an empty chain.
func chainHeadHash(c UserChain) string {
	if len(c.Blocks) == 0 {
		return ""
	}
	h, _ := BlockHash(c.Blocks[len(c.Blocks)-1])
	return h
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
