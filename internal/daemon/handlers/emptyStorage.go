package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// EmptyStorage deletes all files the user has uploaded to the network:
//   - appends "remove" blocks for each file to the network manifest chain
//   - removes local manifest entries, cached files, stubs, and shard data
//   - broadcasts the updated manifest so peers stop routing to these files
func EmptyStorage(req protocol.EmptyStorageRequest) protocol.EmptyStorageResponse {
	fmt.Println("Daemon: deleting all storage.")

	mosaicDir := shared.MosaicDir()
	accountID := helpers.GetAccountID()

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		return protocol.EmptyStorageResponse{
			Success: false,
			Details: fmt.Sprintf("could not load network key: %v", err),
			Username: helpers.GetUsername(),
		}
	}
	kp, err := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if err != nil {
		return protocol.EmptyStorageResponse{
			Success: false,
			Details: fmt.Sprintf("could not load user key: %v", err),
			Username: helpers.GetUsername(),
		}
	}
	nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		return protocol.EmptyStorageResponse{
			Success: false,
			Details: fmt.Sprintf("could not read network manifest: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	files := filesystem.GetUserFiles(nm, accountID)
	if len(files) == 0 {
		return protocol.EmptyStorageResponse{
			Success:          true,
			Details:          "No files to delete.",
			StorageDeleted:   0,
			AvailableStorage: helpers.AvailableStorage(),
			Username:         helpers.GetUsername(),
		}
	}

	bytesFreed := 0
	var deleteErrors []string

	for _, f := range files {
		bytesFreed += f.Size

		// Append remove block to network manifest chain.
		if err := filesystem.AppendBlockRemove(&nm, accountID, f.Name, kp); err != nil {
			deleteErrors = append(deleteErrors, fmt.Sprintf("manifest block for %s: %v", f.Name, err))
		}

		// Remove from local manifest.
		if err := filesystem.RemoveFromManifest(mosaicDir, f.Name); err != nil && !os.IsNotExist(err) {
			fmt.Printf("EmptyStorage: could not remove %s from manifest: %v\n", f.Name, err)
		}

		// Remove stub file.
		if err := filesystem.RemoveStub(mosaicDir, f.Name); err != nil && !os.IsNotExist(err) {
			fmt.Printf("EmptyStorage: could not remove stub for %s: %v\n", f.Name, err)
		}

		// Remove cached real file.
		realPath := filepath.Join(mosaicDir, f.Name)
		if err := os.Remove(realPath); err != nil && !os.IsNotExist(err) {
			fmt.Printf("EmptyStorage: could not remove cached file %s: %v\n", f.Name, err)
		}

		// Remove shard directory for this file.
		shardDir := filepath.Join(transfer.ShardsDir(), f.ContentHash)
		if err := os.RemoveAll(shardDir); err != nil && !os.IsNotExist(err) {
			fmt.Printf("EmptyStorage: could not remove shard dir for %s: %v\n", f.Name, err)
		}
	}

	if len(deleteErrors) > 0 {
		fmt.Printf("EmptyStorage: %d manifest block errors\n", len(deleteErrors))
	}

	// Persist and broadcast the updated manifest.
	if err := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); err != nil {
		return protocol.EmptyStorageResponse{
			Success: false,
			Details: fmt.Sprintf("deleted local data but could not write network manifest: %v", err),
			Username: helpers.GetUsername(),
		}
	}
	BroadcastNetworkManifest(nm)

	gbFreed := bytesFreed / (1024 * 1024 * 1024)
	return protocol.EmptyStorageResponse{
		Success:          true,
		Details:          fmt.Sprintf("Deleted %d file(s) from the network.", len(files)),
		StorageDeleted:   gbFreed,
		AvailableStorage: helpers.AvailableStorage(),
		Username:         helpers.GetUsername(),
	}
}
