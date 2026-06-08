package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// Deletes a file from the network and returns an DeleteFileResponse
// shardHoldersForFile returns the contentHash of filename and the deduplicated
// set of node IDs that hold at least one shard for it (excluding this node).
func shardHoldersForFile(nm filesystem.NetworkManifest, filename string) (contentHash string, holderIDs []string) {
	// Only the owner can delete their own files — scan only the own chain with the meta key.
	kp, err := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if err == nil {
		metaKey := filesystem.MetaKeyFromKP(kp)
		contentHash, _ = filesystem.FindFileByName(nm, helpers.GetAccountID(), filename, &metaKey)
	}
	if contentHash == "" || nm.ShardMap == nil {
		return
	}
	loc, ok := nm.ShardMap[contentHash]
	if !ok {
		return
	}
	seen := make(map[string]bool)
	myNodeID := fmt.Sprintf("%d", helpers.GetNodeID())
	for _, ids := range loc.Holders {
		for _, id := range ids {
			if id != myNodeID && !seen[id] {
				seen[id] = true
				holderIDs = append(holderIDs, id)
			}
		}
	}
	return
}

// signalShardDelete sends a ShardDelete message to each holder node ID.
// Node IDs in the ShardMap are numeric strings; peers are looked up by their
// P2P string ID which is the public key. We broadcast to all peers and let
// each recipient decide whether to act on it.
func signalShardDelete(contentHash string, holderIDs []string) {
	client := GetP2PClient()
	if client == nil {
		return
	}
	msg := api.NewShardDeleteMessage(client.GetID(), contentHash)
	if err := client.SendToAllPeers(msg); err != nil {
		fmt.Printf("Warning: could not broadcast ShardDelete for %s: %v\n", contentHash, err)
	}
}

// Deletes a file from the network and returns an DeleteFileResponse
func DeleteFile(req protocol.DeleteFileRequest) protocol.DeleteFileResponse {
	fmt.Println("Daemon: handling delete for", req.FilePath)

	filename := removePath(req.FilePath)

	mosaicDir := shared.MosaicDir()
	// Remove the stub (if it exists — cached files won't have one).
	if err := filesystem.RemoveStub(mosaicDir, filename); err != nil {
		fmt.Println("Warning: could not remove stub for", filename, "-", err)
	}
	// Remove the real cached file (if it exists).
	realPath := filepath.Join(mosaicDir, filename)
	if _, err := os.Stat(realPath); err == nil {
		if err := os.Remove(realPath); err != nil {
			fmt.Println("Warning: could not remove cached file", filename, "-", err)
		}
	}
	// Remove from manifest.
	if err := filesystem.RemoveFromManifest(mosaicDir, filename); err != nil {
		fmt.Println("Warning: could not update manifest for", filename, "-", err)
	}

	// Update the network manifest: append "remove" block, write, broadcast.
	// Also collect the contentHash and unique shard holders so we can signal deletion.
	var contentHash string
	var holderIDs []string
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			if nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey); err == nil {
				// Capture contentHash and holder set before the remove block erases the entry.
				contentHash, holderIDs = shardHoldersForFile(nm, filename)

				if contentHash == "" {
					fmt.Println("Warning: file not found in network manifest for", filename)
				} else if aerr := filesystem.RecordFileRemove(&nm, helpers.GetAccountID(), contentHash, kp); aerr != nil {
					fmt.Println("Warning: could not record file removal for", filename, "-", aerr)
				} else if werr := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); werr != nil {
					fmt.Println("Warning: could not write network manifest for", filename, "-", werr)
				} else {
					BroadcastNetworkManifest(nm)
				}
			}
		} else {
			fmt.Println("Warning: could not load user key:", kerr)
		}
	}

	// Delete our own local shards for the file.
	if contentHash != "" {
		transfer.DeleteLocalShards(contentHash)
	}

	// Signal all shard holders to delete their copies.
	if contentHash != "" && len(holderIDs) > 0 {
		go signalShardDelete(contentHash, holderIDs)
	}

	return protocol.DeleteFileResponse{
		Success:          true,
		Details:          "Delete processed by daemon",
		FileName:         filename,
		AvailableStorage: helpers.AvailableStorage(),
	}
}
