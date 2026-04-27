package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// ListManifest returns all files in the logged-in user's network manifest
// section, regardless of whether they have local stubs on this machine.
func ListManifest(_ protocol.ListManifestRequest) protocol.ListManifestResponse {
	fmt.Println("Daemon: listing network manifest.")

	if !helpers.IsLoggedIn() {
		return protocol.ListManifestResponse{
			Success: false,
			Details: "not logged in — run 'mos login <key>'",
		}
	}

	mosaicDir := shared.MosaicDir()
	accountID := helpers.GetAccountID()

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		return protocol.ListManifestResponse{
			Success: false,
			Details: fmt.Sprintf("could not load network key: %v", err),
		}
	}

	kp, err := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if err != nil {
		return protocol.ListManifestResponse{
			Success: false,
			Details: fmt.Sprintf("could not load user key: %v", err),
		}
	}

	m, err := filesystem.ReadAndDecryptNetworkManifest(mosaicDir, aesKey, accountID, kp.Private)
	if err != nil {
		return protocol.ListManifestResponse{
			Success: false,
			Details: fmt.Sprintf("could not read network manifest: %v", err),
		}
	}

	idx := filesystem.FindUserIndex(m, accountID)
	if idx == -1 {
		return protocol.ListManifestResponse{
			Success: true,
			Details: "no files in network manifest",
			Files:   []protocol.ManifestFileEntry{},
		}
	}

	localEntries, _ := filesystem.ReadManifest(mosaicDir)

	files := make([]protocol.ManifestFileEntry, 0, len(m.Entries[idx].Files))
	for _, f := range m.Entries[idx].Files {
		cached := false
		if e, ok := localEntries[f.Name]; ok {
			cached = e.Cached
		}
		files = append(files, protocol.ManifestFileEntry{
			Name:      f.Name,
			Size:      f.Size,
			NodeID:    f.PrimaryNodeID,
			DateAdded: f.DateAdded,
			Cached:    cached,
		})
	}

	return protocol.ListManifestResponse{
		Success: true,
		Details: fmt.Sprintf("%d file(s) in network manifest", len(files)),
		Files:   files,
	}
}
