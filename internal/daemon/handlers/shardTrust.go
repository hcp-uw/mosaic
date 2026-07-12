package handlers

import (
	"strconv"
	"sync"
)

// Local proven-liar suppression.
//
// The ShardMap is an unauthenticated, add-only set of claims: any peer can claim
// that any node holds any shard. We do NOT try to remove claims network-wide —
// an unauthenticated global removal would be a censorship vector (a malicious
// node could purge honest holders), and G-set removal doesn't converge anyway.
//
// Instead, when THIS node's storage audit cryptographically disproves a specific
// claim (a peer returns the wrong hash for a shard we hold), we record that
// (holder, file, shard) here and stop trusting it locally — in fetch routing and
// in future probes. This is based only on our own proof, cannot be forged by
// others, and cannot affect any other node's view. It is in-memory per session:
// a genuinely honest node that reconnects is simply re-verified.
var (
	suppressedHolders   = make(map[string]struct{}) // "stunNodeID\x00contentHash\x00shardIndex"
	suppressedHoldersMu sync.RWMutex
)

func suppressedHolderKey(stunNodeID, contentHash string, shardIndex int) string {
	return stunNodeID + "\x00" + contentHash + "\x00" + strconv.Itoa(shardIndex)
}

// SuppressHolder records that stunNodeID was proven NOT to hold shardIndex of
// contentHash, so we stop routing to it and probing it for that shard.
func SuppressHolder(stunNodeID, contentHash string, shardIndex int) {
	if stunNodeID == "" {
		return
	}
	suppressedHoldersMu.Lock()
	suppressedHolders[suppressedHolderKey(stunNodeID, contentHash, shardIndex)] = struct{}{}
	suppressedHoldersMu.Unlock()
}

// IsHolderSuppressed reports whether (stunNodeID, contentHash, shardIndex) has
// been locally disproven.
func IsHolderSuppressed(stunNodeID, contentHash string, shardIndex int) bool {
	suppressedHoldersMu.RLock()
	_, ok := suppressedHolders[suppressedHolderKey(stunNodeID, contentHash, shardIndex)]
	suppressedHoldersMu.RUnlock()
	return ok
}
