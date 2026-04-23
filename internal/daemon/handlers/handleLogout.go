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
// resources: leaves the P2P network, converts cached files back to stubs (so
// manifest entries survive but local bytes are removed), then wipes key files.
func HandleLogout(req protocol.LogoutRequest) protocol.LogoutResponse {
	fmt.Println("Daemon: logging out.")

	// Leave the P2P network gracefully before wiping identity.
	if client := GetP2PClient(); client != nil {
		if err := client.DisconnectFromStun(); err != nil {
			fmt.Printf("Daemon: warning — could not disconnect from network: %v\n", err)
		}
		SetP2PClient(nil)
	}

	// Convert locally cached files to stubs so the manifest entries survive
	// the account switch but the file bytes are removed from disk.
	//
	// Order matters for the watcher:
	//   1. Mark cached=false in manifest first — the watcher's REMOVE snapshot
	//      will capture the updated entry, so the undo re-insert (triggered by
	//      the stub CREATE) writes cached=false back, not cached=true.
	//   2. Delete the real file — fires watcher REMOVE → parks as disappeared.
	//   3. Write stub — fires watcher CREATE → matches disappeared → undo,
	//      re-inserts entry (with cached=false). Manifest stays intact.
	mosaicDir := shared.MosaicDir()
	if entries, err := filesystem.ReadManifest(mosaicDir); err == nil {
		for name, entry := range entries {
			if !entry.Cached {
				continue
			}
			_ = filesystem.MarkUncachedInManifest(mosaicDir, name)
			_ = os.Remove(filepath.Join(mosaicDir, name))
			_ = filesystem.WriteStub(mosaicDir, name, entry.Size, entry.NodeID, entry.ContentHash)
		}
	}

	// Wipe all identity and key material.
	_ = helpers.ClearSession()
	_ = helpers.ClearLoginKey()
	if err := os.Remove(shared.UserKeyPath()); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Daemon: warning — could not delete key file: %v\n", err)
	}

	return protocol.LogoutResponse{
		Success: true,
		Details: "Logged out successfully.",
	}
}
