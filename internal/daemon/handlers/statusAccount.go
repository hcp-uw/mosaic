package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// StatusAccount reports which network nodes are holding shards for files owned
// by this account, along with storage usage statistics.
func StatusAccount(req protocol.StatusAccountRequest) protocol.StatusAccountResponse {
	fmt.Println("Daemon: getting account status.")

	mosaicDir := shared.MosaicDir()
	accountID := helpers.GetAccountID()

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		return protocol.StatusAccountResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not load network key: %v", err),
			Username: helpers.GetUsername(),
		}
	}
	nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		return protocol.StatusAccountResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not read network manifest: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	files := filesystem.GetUserFiles(nm, accountID)

	// Collect unique node IDs that hold at least one shard for any file this account owns.
	seen := make(map[string]struct{})
	for _, f := range files {
		loc, ok := nm.ShardMap[f.ContentHash]
		if !ok {
			continue
		}
		for _, holders := range loc.Holders {
			for _, nodeID := range holders {
				seen[nodeID] = struct{}{}
			}
		}
	}
	nodes := make([]string, 0, len(seen))
	for id := range seen {
		nodes = append(nodes, id)
	}

	return protocol.StatusAccountResponse{
		Success:          true,
		Details:          fmt.Sprintf("%d file(s) owned, shards held by %d node(s).", len(files), len(nodes)),
		Nodes:            nodes,
		GivenStorage:     helpers.AccountGivenStorage(),
		AvailableStorage: helpers.AvailableStorage(),
		UsedStorage:      helpers.UserStorageUsed(),
		Username:         helpers.GetUsername(),
	}
}
