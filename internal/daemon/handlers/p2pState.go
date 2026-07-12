package handlers

import (
	"sync"

	"github.com/hcp-uw/mosaic/internal/api"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// p2pClient is the live P2P client set when the node joins the network.
// Nil when not connected.
var (
	p2pClient   *p2p.Client
	p2pClientMu sync.RWMutex
)

// watcherSuppressFunc, when set, is called by handlers before removing a file
// they generated themselves so the watcher can ignore the resulting event.
// Registered by the daemon package to avoid a circular import.
var watcherSuppressFunc func(path string)

// RegisterWatcherSuppressFunc lets the daemon register its SuppressNext so
// handlers can call it without importing the daemon package.
func RegisterWatcherSuppressFunc(fn func(path string)) {
	watcherSuppressFunc = fn
}

// pendingChallenges maps a nonce hex string to the channel waiting for responses.
// Used by mos status node: the handler registers a channel, broadcasts an
// IdentityChallenge, and reads responses until the deadline.
var (
	pendingChallenges   = make(map[string]chan *api.Message)
	pendingChallengesMu sync.Mutex
)

// RegisterChallenge creates a buffered channel for nonce and returns it.
// The caller must call UnregisterChallenge when done.
func RegisterChallenge(nonce string) chan *api.Message {
	ch := make(chan *api.Message, 32)
	pendingChallengesMu.Lock()
	pendingChallenges[nonce] = ch
	pendingChallengesMu.Unlock()
	return ch
}

// UnregisterChallenge removes the channel registered for nonce.
func UnregisterChallenge(nonce string) {
	pendingChallengesMu.Lock()
	delete(pendingChallenges, nonce)
	pendingChallengesMu.Unlock()
}

// DeliverChallengeResponse routes an IdentityResponse message to the channel
// waiting for its nonce. No-op if no handler is registered for that nonce.
func DeliverChallengeResponse(msg *api.Message) {
	d, err := msg.GetIdentityResponseData()
	if err != nil {
		return
	}
	pendingChallengesMu.Lock()
	ch, ok := pendingChallenges[d.Nonce]
	pendingChallengesMu.Unlock()
	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

// GetP2PClient returns the active P2P client, or nil if not connected.
func GetP2PClient() *p2p.Client {
	p2pClientMu.RLock()
	defer p2pClientMu.RUnlock()
	return p2pClient
}

// SetP2PClient stores the active P2P client so handlers can broadcast manifest
// changes without needing the client passed through every call chain.
func SetP2PClient(c *p2p.Client) {
	p2pClientMu.Lock()
	defer p2pClientMu.Unlock()
	p2pClient = c
}

// BroadcastNetworkManifest sends each connected peer only the manifest records it
// hasn't seen yet (plus the full ShardMap) — see manifestDelta.go. Best-effort.
func BroadcastNetworkManifest(m filesystem.NetworkManifest) {
	broadcastManifestDelta(m, "")
}

// BroadcastNetworkManifestExcluding is BroadcastNetworkManifest but skips
// excludePeerID. Used by handleManifestSync so a merged manifest is not echoed
// back to the peer that just sent it (which would create an infinite ping-pong).
func BroadcastNetworkManifestExcluding(m filesystem.NetworkManifest, excludePeerID string) {
	broadcastManifestDelta(m, excludePeerID)
}
