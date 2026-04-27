package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// SyncUserStubs reads the network manifest, decrypts the logged-in user's
// section, and creates local stubs for any files that aren't already tracked
// in the local manifest. Safe to call when not logged in — exits silently.
//
// Called on: daemon startup, login, and after each manifest sync from a peer.
func SyncUserStubs() {
	mosaicDir := shared.MosaicDir()

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Println("syncUserStubs: could not load network key:", err)
		return
	}

	kp, err := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if err != nil {
		return // not logged in — nothing to sync
	}

	accountID := helpers.GetAccountID()
	m, err := filesystem.ReadAndDecryptNetworkManifest(mosaicDir, aesKey, accountID, kp.Private)
	if err != nil {
		fmt.Println("syncUserStubs: could not read network manifest:", err)
		return
	}

	idx := filesystem.FindUserIndex(m, accountID)
	if idx == -1 {
		return // user has no files in the network manifest yet
	}

	for _, f := range m.Entries[idx].Files {
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
