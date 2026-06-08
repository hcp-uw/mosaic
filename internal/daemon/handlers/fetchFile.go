package handlers

import (
	"fmt"
	"sync"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// inflightFetch is the shared state for concurrent callers waiting on the same file.
type inflightFetch struct {
	done chan struct{}
	data []byte
	err  error
}

// fetchInFlight deduplicates concurrent FetchFileBytes calls for the same filename.
// Without this, two callers racing on the same file overwrite each other's
// shardReadyChans entries, causing one to time out and retry unnecessarily.
var fetchInFlight sync.Map // filename → *inflightFetch

// FetchFileBytes reconstructs a file's raw bytes from its distributed shards.
// If no local shard meta exists yet, it bootstraps from the network manifest
// so that FetchFileBytes can request missing shards from peers.
// Concurrent calls for the same filename are collapsed into a single fetch.
func FetchFileBytes(filename string) ([]byte, error) {
	inflight := &inflightFetch{done: make(chan struct{})}
	actual, loaded := fetchInFlight.LoadOrStore(filename, inflight)
	if loaded {
		// Another goroutine is already fetching this file — wait for it.
		existing := actual.(*inflightFetch)
		<-existing.done
		return existing.data, existing.err
	}
	defer func() {
		fetchInFlight.Delete(filename)
		close(inflight.done) // unblock all waiters
	}()
	inflight.data, inflight.err = doFetchFileBytes(filename)
	return inflight.data, inflight.err
}

func doFetchFileBytes(filename string) ([]byte, error) {
	client := GetP2PClient()
	mosaicDir := shared.MosaicDir()

	// Load the manifest once — used for both the holder lookup and meta bootstrap.
	var nm filesystem.NetworkManifest
	var manifestLoaded bool
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err == nil {
		m, merr := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
		if merr != nil {
			// Manifest is corrupt — use an empty one so getHolders is still set and
			// ShardRequests can be sent to peers (they will respond if they hold shards).
			m = filesystem.NetworkManifest{}
		}
		nm = m
		manifestLoaded = true
	}

	// If no local shard meta exists for this file, synthesise one from the
	// manifest so FetchFileBytes can proceed to request shards from peers
	// rather than failing immediately on a fresh node.
	if transfer.FindShardMeta(filename) == nil && manifestLoaded {
		if kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath()); kerr == nil {
			metaKey := filesystem.MetaKeyFromKP(kp)
			accountID := helpers.GetAccountID()
			for _, f := range filesystem.GetUserFiles(nm, accountID, &metaKey) {
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
