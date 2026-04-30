package handlers

import (
	"fmt"
	"sync"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/p2p"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// p2pClient is the live P2P client set when the node joins the network.
// Nil when not connected.
var (
	p2pClient   *p2p.Client
	p2pClientMu sync.RWMutex
)

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

// BroadcastNetworkManifest serializes m and sends it to all connected peers.
// Errors are logged but do not propagate — broadcast is best-effort.
func BroadcastNetworkManifest(m filesystem.NetworkManifest) {
	p2pClientMu.RLock()
	c := p2pClient
	p2pClientMu.RUnlock()

	if c == nil || !c.IsPeerCommunicationAvailable() {
		return
	}

	data, err := filesystem.ManifestToJSON(m)
	if err != nil {
		fmt.Println("BroadcastNetworkManifest: could not serialize manifest:", err)
		return
	}

	msg := api.NewManifestSyncMessage(data)

	if err := c.SendToAllPeers(msg); err != nil {
		fmt.Println("BroadcastNetworkManifest: send error:", err)
	}
}
