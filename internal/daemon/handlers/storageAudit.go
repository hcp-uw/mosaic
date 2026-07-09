package handlers

import (
	"fmt"
	"os"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// The storage audit activates the storage-proof (ShardProbe) mechanism, which is
// otherwise never initiated. Periodically it challenges connected peers to prove
// they still hold shards they claim in the ShardMap, for shards we also hold
// locally (so we can compute the expected hash). transfer.ProbeShardAtPeer records
// the result; three consecutive failures evict the peer via RecordProbeResult.
//
// The audit only works because shard encryption is deterministic: an honest
// holder's copy is byte-identical to ours, so SHA-256(nonce ‖ bytes) matches.
const (
	storageAuditInterval = 2 * time.Minute
	// maxProbesPerAudit bounds a single pass so the audit is a light background
	// integrity check, not a probe storm — the network stays cheap to run.
	maxProbesPerAudit = 8
)

// startStorageAudit runs the periodic storage-proof audit for the lifetime of
// the client. Started once from runClient after a successful join.
func startStorageAudit(client *p2p.Client) {
	ticker := time.NewTicker(storageAuditInterval)
	defer ticker.Stop()
	for range ticker.C {
		if client == nil || !client.IsPeerCommunicationAvailable() {
			continue
		}
		runStorageAuditPass(client)
	}
}

// runStorageAuditPass probes up to maxProbesPerAudit (shard, peer) pairs where
// the manifest lists a connected peer as a holder and we hold the shard locally.
func runStorageAuditPass(client *p2p.Client) {
	mosaicDir := shared.MosaicDir()
	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		return
	}
	nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		return
	}

	// Map each connected peer's stable STUN node ID → its live P2P id, so we can
	// resolve a manifest holder (recorded by stable id) to a peer we can reach.
	connectedByStunID := make(map[string]string)
	for _, p := range client.GetConnectedPeers() {
		if p.StunNodeID != "" {
			connectedByStunID[p.StunNodeID] = p.ID
		}
	}
	if len(connectedByStunID) == 0 {
		return
	}

	entries, err := os.ReadDir(transfer.ShardsDir())
	if err != nil {
		return
	}

	probes := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fileHash := e.Name()
		meta := transfer.FindShardMetaByHash(fileHash)
		if meta == nil {
			continue
		}
		for shardIdx := 0; shardIdx < meta.TotalShards; shardIdx++ {
			// Only probe shards we hold locally — we need our copy to compute the
			// expected hash the peer must match.
			shardPath := fmt.Sprintf("%s/%s/shard%d_%s.dat",
				transfer.ShardsDir(), fileHash, shardIdx, fileHash)
			if _, serr := os.Stat(shardPath); serr != nil {
				continue
			}
			for _, holderStunID := range filesystem.GetShardHolders(nm, fileHash, shardIdx) {
				peerP2PID, connected := connectedByStunID[holderStunID]
				if !connected {
					continue // only probe holders we can actually reach right now
				}
				if IsHolderSuppressed(holderStunID, fileHash, shardIdx) {
					continue // already proven a liar for this shard — don't re-probe
				}
				if probes >= maxProbesPerAudit {
					return
				}
				probes++
				go func(fh, holderID, p2pID string, idx int) {
					// A proven wrong hash means this holder does not actually have
					// the shard it claims — suppress it locally so we stop trusting
					// and re-probing it. A timeout is inconclusive and not suppressed.
					if transfer.ProbeShardAtPeer(fh, idx, p2pID, client) == transfer.ProbeWrongHash {
						SuppressHolder(holderID, fh, idx)
						fmt.Printf("[Audit] holder %s failed proof for shard %d of %s — suppressed locally\n",
							transfer.ShortHash(holderID), idx, transfer.ShortHash(fh))
					}
				}(fileHash, holderStunID, peerP2PID, shardIdx)
			}
		}
	}
}
