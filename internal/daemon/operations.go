package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/crypto"
	"github.com/hcp-uw/mosaic/internal/manifest"
	"github.com/hcp-uw/mosaic/internal/storage"
	"github.com/klauspost/reedsolomon"
)

// storeRequestTimeout bounds how long upload waits for each peer ack.
const storeRequestTimeout = 5 * time.Second


// UploadFile encrypts, RS-encodes, and distributes the file at path.
//
// On success the file is recorded in the manifest with its shard hashes
// and the wrapped per-file key, and a manifest update is broadcast to
// connected peers.
func (a *App) UploadFile(path string) (*manifest.FileMeta, error) {
	plain, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	fileKey, err := crypto.GenerateFileKey()
	if err != nil {
		return nil, err
	}
	sealed, err := crypto.EncryptFile(fileKey, plain)
	if err != nil {
		return nil, err
	}

	enc, err := reedsolomon.New(DefaultDataShards, DefaultParityShards)
	if err != nil {
		return nil, fmt.Errorf("reedsolomon: %w", err)
	}
	shards, err := enc.Split(sealed)
	if err != nil {
		return nil, fmt.Errorf("rs split: %w", err)
	}
	if err := enc.Encode(shards); err != nil {
		return nil, fmt.Errorf("rs encode: %w", err)
	}

	wrapped, err := crypto.WrapKeyForOwner(fileKey, a.Identity.Private)
	if err != nil {
		return nil, err
	}

	// Hash every shard — shards are NOT stored locally; they live only on peers.
	shardHashes := make([][32]byte, len(shards))
	for i, s := range shards {
		shardHashes[i] = sha256.Sum256(s)
	}

	meta := manifest.FileMeta{
		Filename:      filepath.Base(path),
		Size:          uint64(len(plain)),
		EncryptedSize: uint64(len(sealed)),
		DataShards:    uint32(DefaultDataShards),
		ParityShards:  uint32(DefaultParityShards),
		BlockSize:     uint32(len(shards[0])),
		Shards:        shardHashes,
		WrappedKey:    wrapped,
	}
	meta.Sign(a.Identity.Private)

	if err := a.Manifest.AddFile(meta); err != nil {
		return nil, fmt.Errorf("manifest add file: %w", err)
	}

	// Distribute all shards across connected peers (best effort).
	a.distributeShards(shards)

	// Gossip new entries.
	a.broadcastManifestEntries(meta)
	return &meta, nil
}

func (a *App) distributeShards(shards [][]byte) {
	if a.P2P == nil {
		return
	}
	peers := a.P2P.GetConnectedPeers()
	if len(peers) == 0 {
		return
	}
	// Round-robin assign each shard to one peer.
	peerIDs := make([]string, len(peers))
	for i, p := range peers {
		peerIDs[i] = p.ID
	}
	sort.Strings(peerIDs)
	for i, s := range shards {
		peer := peerIDs[i%len(peerIDs)]
		req := api.NewSignedStoreRequest(a.Identity.Private, s)
		ack, err := a.P2P.SendStoreRequest(peer, req, storeRequestTimeout)
		if err != nil {
			continue
		}
		if err := ack.Verify(); err != nil {
			continue
		}
		var sh storage.Hash
		copy(sh[:], ack.Hash)
		peerHex := fmt.Sprintf("%x", ack.PublicKey)
		a.P2P.RegisterPubkeyMapping(peerHex, peer)
		_ = a.Manifest.MarkShardStored(sh, peerHex, ack.Timestamp)
	}
}

func (a *App) broadcastManifestEntries(meta manifest.FileMeta) {
	if a.P2P == nil {
		return
	}
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		return
	}
	update := api.PeerManifestUpdateData{
		FileMetas: []json.RawMessage{rawMeta},
	}
	_ = a.P2P.BroadcastManifestUpdate(update)
}

// DownloadFile reconstructs the file identified by filename for the
// current owner and writes it to outPath.
func (a *App) DownloadFile(filename, outPath string) error {
	id := manifest.FileID(a.Identity.Public, filename)
	meta, ok := a.Manifest.Manifest().File(id)
	if !ok {
		return fmt.Errorf("file %q not in manifest", filename)
	}
	if meta.Tombstone {
		return fmt.Errorf("file %q has been deleted", filename)
	}

	enc, err := reedsolomon.New(int(meta.DataShards), int(meta.ParityShards))
	if err != nil {
		return fmt.Errorf("reedsolomon: %w", err)
	}

	total := int(meta.DataShards + meta.ParityShards)
	shards := make([][]byte, total)
	have := 0

	for i, h := range meta.Shards {
		var sh storage.Hash
		copy(sh[:], h[:])
		if a.Store.Has(sh) {
			data, err := a.Store.Get(sh)
			if err == nil {
				shards[i] = data
				have++
				continue
			}
		}
		// Fetch from a peer. Replicas() is sorted; try each in order.
		replicas := a.Manifest.Manifest().Replicas(h)
		for _, peerHex := range replicas {
			if peerHex == a.Identity.PublicKeyHex() {
				continue
			}
			if a.P2P == nil {
				break
			}
			resp, err := a.P2P.SendGetRequest(peerHex, &api.GetRequest{Hash: h[:]}, a.GetRequestTimeout)
			if err != nil || len(resp.Data) == 0 {
				continue
			}
			// Validate: the bytes must hash to h.
			sum := sha256.Sum256(resp.Data)
			if sum != h {
				continue
			}
			shards[i] = resp.Data
			have++
			break
		}
	}

	if have < int(meta.DataShards) {
		return fmt.Errorf("only %d/%d shards available; cannot reconstruct", have, meta.DataShards)
	}

	if err := enc.ReconstructData(shards); err != nil {
		return fmt.Errorf("reconstruct: %w", err)
	}

	// Reassemble first DataShards into the sealed ciphertext, then trim
	// to the recorded EncryptedSize (RS pads to a multiple of shard size).
	sealed := make([]byte, 0, len(shards[0])*int(meta.DataShards))
	for i := 0; i < int(meta.DataShards); i++ {
		sealed = append(sealed, shards[i]...)
	}
	if uint64(len(sealed)) > meta.EncryptedSize {
		sealed = sealed[:meta.EncryptedSize]
	}

	fileKey, err := crypto.UnwrapKey(meta.WrappedKey, a.Identity.Private)
	if err != nil {
		return fmt.Errorf("unwrap key: %w", err)
	}
	plain, err := crypto.DecryptFile(fileKey, sealed)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	if uint64(len(plain)) > meta.Size {
		plain = plain[:meta.Size]
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return fmt.Errorf("mkdir out: %w", err)
	}
	if err := os.WriteFile(outPath, plain, 0o600); err != nil {
		return fmt.Errorf("write out: %w", err)
	}
	return nil
}

// DeleteFile tombstones the file and broadcasts shard delete contracts.
func (a *App) DeleteFile(filename string) error {
	id := manifest.FileID(a.Identity.Public, filename)
	meta, ok := a.Manifest.Manifest().File(id)
	if !ok {
		return fmt.Errorf("file %q not in manifest", filename)
	}

	now := time.Now().UTC()
	if err := a.Manifest.MarkFileDeleted(id, now); err != nil {
		return err
	}
	for _, h := range meta.Shards {
		var sh storage.Hash
		copy(sh[:], h[:])
		_ = a.Store.Delete(sh) // best effort
		if err := a.Manifest.MarkShardDeleted(sh, now); err != nil {
			return err
		}
		req := api.NewSignedDeleteRequest(a.Identity.Private, h[:])
		if a.P2P != nil {
			_ = a.P2P.BroadcastDeleteRequest(req)
		}
	}
	return nil
}

// ListFiles returns every file owned by the current identity (sorted).
func (a *App) ListFiles() []manifest.FileMeta {
	return a.Manifest.Manifest().FilesOwnedBy(a.Identity.Public)
}

// FileInfo returns a single file's metadata, or an error if missing.
func (a *App) FileInfo(filename string) (manifest.FileMeta, error) {
	id := manifest.FileID(a.Identity.Public, filename)
	meta, ok := a.Manifest.Manifest().File(id)
	if !ok {
		return manifest.FileMeta{}, errors.New("file not found")
	}
	if meta.Tombstone {
		return manifest.FileMeta{}, errors.New("file deleted")
	}
	return meta, nil
}

// EmptyStorage tombstones every file owned by us and removes all locally
// stored shards. Returns the number of bytes freed.
func (a *App) EmptyStorage() (uint64, error) {
	freed := a.Store.UsedBytes()
	for _, f := range a.ListFiles() {
		if err := a.DeleteFile(f.Filename); err != nil {
			return 0, err
		}
	}
	// Also drop any shards we hold for other owners (they will resync
	// via their own delete requests if needed).
	hashes, err := a.Store.List()
	if err != nil {
		return 0, err
	}
	for _, h := range hashes {
		_ = a.Store.Delete(h)
	}
	return freed, nil
}
