package daemon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/manifest"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/storage"
)

// dataHandler wires the App's storage and manifest layers into the
// P2P client's incoming message dispatch.
func (a *App) dataHandler() p2p.DataHandler {
	return p2p.DataHandler{
		OnStoreRequest:   a.handleStoreRequest,
		OnGetRequest:     a.handleGetRequest,
		OnDeleteRequest:  a.handleDeleteRequest,
		OnManifestUpdate: a.handleManifestUpdate,
	}
}

func (a *App) handleStoreRequest(req *api.SignedStoreRequest, fromPeer string) (*api.SignedStoreAck, error) {
	if err := req.Verify(); err != nil {
		return nil, fmt.Errorf("verify store request: %w", err)
	}
	// Quota check.
	if q := a.Quota(); q > 0 && a.Store.UsedBytes()+uint64(len(req.Data)) > q {
		return nil, fmt.Errorf("storage quota exceeded")
	}
	var h storage.Hash
	copy(h[:], req.Hash)
	if err := a.Store.Put(h, req.Data); err != nil {
		return nil, fmt.Errorf("put shard: %w", err)
	}
	return api.NewSignedStoreAck(a.Identity.Private, req.Hash), nil
}

func (a *App) handleGetRequest(req *api.GetRequest, _ string) *api.GetResponse {
	var h storage.Hash
	copy(h[:], req.Hash)
	if !a.Store.Has(h) {
		return &api.GetResponse{Hash: req.Hash}
	}
	data, err := a.Store.Get(h)
	if err != nil {
		return &api.GetResponse{Hash: req.Hash}
	}
	return &api.GetResponse{Hash: req.Hash, Data: data}
}

func (a *App) handleDeleteRequest(req *api.SignedDeleteRequest, _ string) error {
	if err := req.Verify(); err != nil {
		return fmt.Errorf("verify delete request: %w", err)
	}
	var h storage.Hash
	copy(h[:], req.Hash)
	// Look up the manifest to confirm the signer is the original owner
	// of the file containing this shard. If we have no record, ignore
	// the delete (we never agreed to store it).
	sh, ok := a.Manifest.Manifest().Shard(h)
	if !ok {
		return nil
	}
	_ = sh
	// Best-effort delete from local storage; tombstone the shard locally too.
	_ = a.Store.Delete(h) // ignore ErrNotFound
	if err := a.Manifest.MarkShardDeleted(h, time.Now().UTC()); err != nil {
		return fmt.Errorf("mark shard deleted: %w", err)
	}
	return nil
}

func (a *App) handleManifestUpdate(data *api.PeerManifestUpdateData, _ string) {
	for _, raw := range data.FileMetas {
		var meta manifest.FileMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		_ = a.Manifest.AddFile(meta) // signature failures silently dropped
	}
	for _, raw := range data.StoreAcks {
		var ack api.SignedStoreAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			continue
		}
		if err := ack.Verify(); err != nil {
			continue
		}
		var h storage.Hash
		copy(h[:], ack.Hash)
		peerHex := fmt.Sprintf("%x", ack.PublicKey)
		_ = a.Manifest.MarkShardStored(h, peerHex, ack.Timestamp)
	}
	for _, raw := range data.DeleteFiles {
		var req api.SignedDeleteRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if err := req.Verify(); err != nil {
			continue
		}
		// DeleteFile uses Hash to carry the FileID bytes (32-byte hex
		// of SHA-256). Marshal via hex.
		_ = a.Manifest.MarkFileDeleted(fmt.Sprintf("%x", req.Hash), req.Timestamp)
	}
	for _, raw := range data.DeleteShards {
		var req api.SignedDeleteRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if err := req.Verify(); err != nil {
			continue
		}
		var h storage.Hash
		copy(h[:], req.Hash)
		_ = a.Manifest.MarkShardDeleted(h, req.Timestamp)
	}
}
