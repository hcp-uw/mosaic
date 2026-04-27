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

	m, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		return protocol.ListManifestResponse{
			Success: false,
			Details: fmt.Sprintf("could not read network manifest: %v", err),
		}
	}

	idx := filesystem.FindChainIndex(m, accountID)
	if idx == -1 {
		return protocol.ListManifestResponse{
			Success: true,
			Details: "no files in network manifest",
			Files:   []protocol.ManifestFileEntry{},
		}
	}

	networkFiles := filesystem.ChainToFiles(m.Chains[idx])
	localEntries, _ := filesystem.ReadManifest(mosaicDir)

	files := make([]protocol.ManifestFileEntry, 0, len(networkFiles))
	for _, f := range networkFiles {
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
