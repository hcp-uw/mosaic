package handlers

import (
	"fmt"
	"os"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// SyncUserStubs reads the network manifest, replays the logged-in user's chain,
// and creates local stubs for any files not already tracked in the local manifest.
// Safe to call when not logged in — exits silently.
//
// Called on: daemon startup, login, and after each manifest sync from a peer.
func SyncUserStubs() {
	mosaicDir := shared.MosaicDir()

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Println("syncUserStubs: could not load network key:", err)
		return
	}

	if !helpers.IsLoggedIn() {
		return // not logged in — nothing to sync
	}

	accountID := helpers.GetAccountID()
	m, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Println("syncUserStubs: could not read network manifest:", err)
		return
	}

	idx := filesystem.FindChainIndex(m, accountID)
	if idx == -1 {
		return // user has no files in the network manifest yet
	}

	files := filesystem.ChainToFiles(m.Chains[idx])

	// Build a set of currently-live names so we can detect deletions.
	networkNames := make(map[string]bool, len(files))
	for _, f := range files {
		networkNames[f.Name] = true
	}

	// Remove local entries for files deleted from the network manifest.
	localEntries, lerr := filesystem.ReadManifest(mosaicDir)
	if lerr == nil {
		for name := range localEntries {
			if !networkNames[name] {
				if err := filesystem.RemoveFromManifest(mosaicDir, name); err != nil {
					fmt.Printf("syncUserStubs: could not remove %s from local manifest: %v\n", name, err)
				}
				if err := filesystem.RemoveStub(mosaicDir, name); err != nil && !os.IsNotExist(err) {
					fmt.Printf("syncUserStubs: could not remove stub for %s: %v\n", name, err)
				}
				fmt.Printf("syncUserStubs: removed deleted file %s from local state\n", name)
			}
		}
	}

	// Add stubs for files not yet tracked locally.
	for _, f := range files {
		if filesystem.IsInManifest(mosaicDir, f.Name) {
			continue
		}
		if err := filesystem.AddToManifest(mosaicDir, f.Name, f.Size, accountID, f.ContentHash); err != nil {
			fmt.Printf("syncUserStubs: could not add %s to manifest: %v\n", f.Name, err)
			continue
		}
		if err := filesystem.WriteStub(mosaicDir, f.Name, f.Size, accountID, f.ContentHash); err != nil {
			fmt.Printf("syncUserStubs: could not write stub for %s: %v\n", f.Name, err)
			continue
		}
		fmt.Printf("syncUserStubs: created stub for %s\n", f.Name)
	}
}
