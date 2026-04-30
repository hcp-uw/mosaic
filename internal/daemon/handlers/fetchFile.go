package handlers

import (
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// FetchFileBytes reconstructs a file's raw bytes from its distributed shards.
// If no local shard meta exists yet, it bootstraps from the network manifest
// so that FetchFileBytes can request missing shards from peers.
func FetchFileBytes(filename string) ([]byte, error) {
	client := GetP2PClient()
	mosaicDir := shared.MosaicDir()

	// Load the manifest once — used for both the holder lookup and meta bootstrap.
	var nm filesystem.NetworkManifest
	var manifestLoaded bool
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		if m, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey); err == nil {
			nm = m
			manifestLoaded = true
		}
	}

	// If no local shard meta exists for this file, synthesise one from the
	// manifest so FetchFileBytes can proceed to request shards from peers
	// rather than failing immediately on a fresh node.
	if transfer.FindShardMeta(filename) == nil && manifestLoaded {
		for _, chain := range nm.Chains {
			for _, f := range filesystem.ChainToFiles(chain) {
				if f.Name == filename {
					transfer.EnsureShardMeta(f.ContentHash, f.Name, f.Size)
					break
				}
			}
		}
		if transfer.FindShardMeta(filename) == nil {
			return nil, fmt.Errorf("%q not found in network manifest", filename)
		}
	}

	// Build shard-holder lookup from the manifest.
	var getHolders func(contentHash string, shardIndex int) []string
	if client != nil && manifestLoaded {
		getHolders = func(contentHash string, shardIndex int) []string {
			return filesystem.GetShardHolders(nm, contentHash, shardIndex)
		}
	}

	return transfer.FetchFileBytes(filename, client, getHolders)
}
