package handlers

import (
	"strconv"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// Manifest delta sync.
//
// A ManifestSync used to carry the ENTIRE network manifest on every change. With
// the LWW-Set CRDT each FileRecord has a monotonic Seq, so we can send a peer only
// the records newer than what it already has. We track, per peer, the highest Seq
// we believe it holds for each user, and send just the delta.
//
// This is OPTIMISTIC — a record we send is assumed delivered — so a delta lost
// over UDP would be missed. A periodic FULL sync (startManifestFullSync) heals any
// gaps. CRDT merges are idempotent, so re-sending is always safe; the only risk
// delta sync adds is under-sending, which the backstop covers.
//
// The ShardMap (a G-set with no Seq) is sent in full in every message — only the
// per-user file records are delta'd. A coarse ShardMap version could delta it too
// later, but records are the frequent, growing part.

const manifestFullSyncInterval = 60 * time.Second

var (
	// manifestSeen[peerID][recordKey] = highest FileRecord Seq we believe that peer
	// holds for that record. Seq is per (userID, contentHash) — a new file starts at
	// Seq 1 and increments only on re-add/rename of that same file — so tracking must
	// be per record, NOT a per-user max (a per-user max would wrongly exclude a new
	// file whose Seq is below another file's, so it would never sync).
	manifestSeen   = make(map[string]map[string]uint64)
	manifestSeenMu sync.Mutex
)

// recordKey uniquely identifies a FileRecord across users (two users could hold the
// same contentHash with independent Seq counters).
func recordKey(userID int, contentHash string) string {
	return strconv.Itoa(userID) + "\x00" + contentHash
}

// manifestDeltaFor builds the manifest to send to peerID: only the file records
// newer than what the peer has seen, plus the full ShardMap. A user with no newer
// records is omitted entirely. Returns the delta and whether it holds any records.
func manifestDeltaFor(peerID string, m filesystem.NetworkManifest) (filesystem.NetworkManifest, bool) {
	manifestSeenMu.Lock()
	seen := manifestSeen[peerID] // nil is fine: reads yield 0
	manifestSeenMu.Unlock()

	delta := filesystem.NetworkManifest{
		Version:  m.Version,
		Users:    make(map[int]*filesystem.UserState),
		ShardMap: m.ShardMap, // sent in full — no Seq to delta on
	}
	hasNew := false
	for userID, us := range m.Users {
		if us == nil {
			continue
		}
		var newer map[string]*filesystem.FileRecord
		for hash, r := range us.Records {
			if r == nil || r.Seq <= seen[recordKey(userID, hash)] {
				continue
			}
			if newer == nil {
				newer = make(map[string]*filesystem.FileRecord)
			}
			newer[hash] = r
			hasNew = true
		}
		if newer != nil {
			delta.Users[userID] = &filesystem.UserState{
				UserID:    us.UserID,
				Username:  us.Username,
				PublicKey: us.PublicKey, // required so the receiver can verify signatures on merge
				Records:   newer,
			}
		}
	}
	return delta, hasNew
}

// noteManifestSent records that peerID now holds every record in m at its current
// Seq (a delta includes every record newer than what the peer had seen), so future
// deltas skip them.
func noteManifestSent(peerID string, m filesystem.NetworkManifest) {
	manifestSeenMu.Lock()
	defer manifestSeenMu.Unlock()
	seen := manifestSeen[peerID]
	if seen == nil {
		seen = make(map[string]uint64)
		manifestSeen[peerID] = seen
	}
	for userID, us := range m.Users {
		if us == nil {
			continue
		}
		for hash, r := range us.Records {
			if r == nil {
				continue
			}
			if key := recordKey(userID, hash); r.Seq > seen[key] {
				seen[key] = r.Seq
			}
		}
	}
}

// noteManifestReceived records that peerID demonstrably holds the records in m (it
// just sent them to us), so we won't echo them back.
func noteManifestReceived(peerID string, m filesystem.NetworkManifest) {
	noteManifestSent(peerID, m)
}

// forgetManifestPeer drops a departed peer's delta state so a reconnection starts
// with a full sync.
func forgetManifestPeer(peerID string) {
	manifestSeenMu.Lock()
	delete(manifestSeen, peerID)
	manifestSeenMu.Unlock()
}

// broadcastManifestDelta sends each connected peer (except excludePeerID) the
// records it hasn't seen yet plus the full ShardMap.
func broadcastManifestDelta(m filesystem.NetworkManifest, excludePeerID string) {
	c := GetP2PClient()
	if c == nil {
		return
	}
	for _, peer := range c.GetConnectedPeers() {
		if peer.ID == excludePeerID {
			continue
		}
		delta, _ := manifestDeltaFor(peer.ID, m)
		data, err := filesystem.ManifestToJSON(delta)
		if err != nil {
			continue
		}
		if err := c.SendReliableToPeer(peer.ID, api.NewManifestSyncMessage(c.GetID(), data)); err == nil {
			noteManifestSent(peer.ID, m)
		}
	}
}

// sendFullManifestToPeer sends the entire manifest to one peer — used on first
// contact and by the periodic full-sync backstop.
func sendFullManifestToPeer(peerID string, m filesystem.NetworkManifest) {
	c := GetP2PClient()
	if c == nil {
		return
	}
	data, err := filesystem.ManifestToJSON(m)
	if err != nil {
		return
	}
	if err := c.SendReliableToPeer(peerID, api.NewManifestSyncMessage(c.GetID(), data)); err == nil {
		noteManifestSent(peerID, m)
	}
}

// startManifestFullSync periodically sends the full manifest to every peer,
// healing any deltas lost over UDP. Runs for the client's lifetime.
func startManifestFullSync(client *p2p.Client) {
	ticker := time.NewTicker(manifestFullSyncInterval)
	defer ticker.Stop()
	for range ticker.C {
		if client == nil || !client.IsPeerCommunicationAvailable() {
			continue
		}
		aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
		if err != nil {
			continue
		}
		m, err := filesystem.ReadNetworkManifest(shared.MosaicDir(), aesKey)
		if err != nil {
			continue
		}
		for _, peer := range client.GetConnectedPeers() {
			sendFullManifestToPeer(peer.ID, m)
		}
	}
}
