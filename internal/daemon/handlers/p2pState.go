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
