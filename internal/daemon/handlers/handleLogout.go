package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// HandleLogout clears all local identity state and cleans up account-specific
// resources: broadcasts the final network manifest, leaves the P2P network,
// removes all local file data and stubs, clears the local manifest, then wipes
// key files. The next account to log in starts with a completely clean slate.
func HandleLogout(req protocol.LogoutRequest) protocol.LogoutResponse {
	fmt.Println("Daemon: logging out.")

	mosaicDir := shared.MosaicDir()

	// Broadcast the network manifest one final time before disconnecting so
	// peers receive any pending changes before we drop off the network.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey); err == nil {
			BroadcastNetworkManifest(nm)
		}
	}

	// Leave the P2P network gracefully.
	if client := GetP2PClient(); client != nil {
		if err := client.DisconnectFromStun(); err != nil {
			fmt.Printf("Daemon: warning — could not disconnect from network: %v\n", err)
		}
		SetP2PClient(nil)
	}

	// Remove locally cached file bytes. For each cached file, go through the
	// watcher-safe sequence before deleting the stub:
	//   1. Mark cached=false — watcher REMOVE snapshot captures this state.
	//   2. Delete the real file — watcher REMOVE fires, parks as disappeared.
	//   3. Write stub — watcher CREATE fires, undoes the disappear.
	// Then delete the stub immediately — next account must not see these files.
	if entries, err := filesystem.ReadManifest(mosaicDir); err == nil {
		for name, entry := range entries {
			if entry.Cached {
				_ = filesystem.MarkUncachedInManifest(mosaicDir, name)
				_ = os.Remove(filepath.Join(mosaicDir, name))
				_ = filesystem.WriteStub(mosaicDir, name, entry.Size, entry.NodeID, entry.ContentHash)
			}
		}
	}

	// Delete all stubs — the next account must not inherit this account's file list.
	if err := filesystem.RemoveAllStubs(mosaicDir); err != nil {
		fmt.Printf("Daemon: warning — could not remove stubs: %v\n", err)
	}

	// Clear the local manifest so the next account starts with an empty file list.
	if err := filesystem.ClearManifest(mosaicDir); err != nil {
		fmt.Printf("Daemon: warning — could not clear manifest: %v\n", err)
	}

	// Wipe all identity and key material.
	_ = helpers.ClearSession()
	_ = helpers.ClearLoginKey() // no-op if already absent; kept for old installations
	for _, keyFile := range []string{shared.UserKeyPath(), shared.ShardKeyPath()} {
		if err := os.Remove(keyFile); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Daemon: warning — could not delete key file %s: %v\n", keyFile, err)
		}
	}

	return protocol.LogoutResponse{
		Success: true,
		Details: "Logged out successfully.",
	}
}
