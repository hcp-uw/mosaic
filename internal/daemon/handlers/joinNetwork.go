package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// HandleJoin joins the P2P network, verifying the STUN connection before returning.
func HandleJoin(req protocol.JoinRequest) protocol.JoinResponse {
	fmt.Println("Daemon: joining network.")

	errCh := make(chan error, 1)
	go runClient(req.ServerAddress, errCh)

	if err := <-errCh; err != nil {
		return protocol.JoinResponse{Success: false, Details: fmt.Sprintf("failed to connect to STUN server: %v", err)}
	}

	return protocol.JoinResponse{Success: true, Details: "Joined network successfully."}
}

func runClient(serverAddr string, errCh chan<- error) {
	mosaicDir := shared.MosaicDir()

	config := p2p.DefaultClientConfig(serverAddr, shared.DefaultTURNServer, shared.TURNUsername, shared.TURNPassword)
	client, err := p2p.NewClient(config)
	if err != nil {
		log.Printf("Failed to create P2P client: %v", err)
		errCh <- err
		return
	}

	// Expose the client so upload/broadcast handlers can reach it.
	SetP2PClient(client)

	// Start the shared send rate-limiter used by UploadFile.
	transfer.Init(context.Background())

	// Register the shard-stored callback so the manifest is updated and
	// broadcast each time this node stores a new shard (upload or receive).
	transfer.SetShardStoredCallback(func(contentHash string, shardIndex int) {
		recordShardInManifest(contentHash, shardIndex)
	})

	client.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("[P2P] State: %s\n", state)
	})

	client.OnPeerAssigned(func(peer *p2p.PeerInfo) {
		fmt.Printf("[P2P] Peer assigned: %s (%s)\n", peer.ID, peer.Address)
		if err := client.ConnectToPeer(peer); err != nil {
			fmt.Printf("[P2P] Failed to connect to peer: %v\n", err)
			return
		}
		fmt.Printf("[P2P] Connected to peer %s\n", peer.ID)
		go pushManifestToPeer(mosaicDir, client)
		go redistributeShardsToNewPeer(peer, client)
		go announceIdentity(client)
	})

	client.OnPeerLeft(func(peerID string) {
		fmt.Printf("[P2P] Peer left: %s\n", peerID)
		go handlePeerLeft(peerID, mosaicDir, client)
	})

	client.OnError(func(err error) {
		fmt.Printf("[P2P] Error: %v\n", err)
	})

	client.OnMessageReceived(func(data []byte) {
		// Binary shard frame — skip JSON parsing entirely.
		if len(data) > 0 && data[0] == 0x01 {
			go transfer.HandleBinaryShardChunk(data)
			return
		}
		msg, err := api.DeserializeMessage(data)
		if err != nil {
			return
		}
		switch msg.Type {
		case api.ManifestSync:
			go handleManifestSync(mosaicDir, msg)
		case api.ShardRequest:
			go transfer.HandleShardRequest(msg, client)
		case api.ShardResponse:
			go handleShardResponse(msg)
		case api.IdentityAnnounce:
			// nothing to store — pubkey is in msg.Sign.PubKey, used passively
		case api.IdentityChallenge:
			go handleIdentityChallenge(msg, client)
		case api.IdentityResponse:
			go DeliverChallengeResponse(msg)
		}
	})

	fmt.Printf("[P2P] Connecting to STUN server at %s…\n", serverAddr)
	if err := client.ConnectToStun(); err != nil {
		log.Printf("Failed to connect to STUN: %v", err)
		SetP2PClient(nil)
		errCh <- err
		return
	}
	errCh <- nil
	fmt.Println("[P2P] Connected. Waiting for peers.")
}

// pushManifestToPeer sends our local network manifest to a newly connected peer.
// Retries until a peer is reachable (ConnectToPeer is async so Conn may not be
// set yet when OnPeerAssigned fires).
func pushManifestToPeer(mosaicDir string, client *p2p.Client) {
	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Println("pushManifestToPeer: could not load network key:", err)
		return
	}
	m, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Println("pushManifestToPeer: could not read manifest:", err)
		return
	}
	data, err := filesystem.ManifestToJSON(m)
	if err != nil {
		fmt.Println("pushManifestToPeer: could not serialize manifest:", err)
		return
	}

	msg := api.NewManifestSyncMessage(data)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.SendToAllPeers(msg); err == nil {
			fmt.Println("pushManifestToPeer: manifest pushed to peer")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("pushManifestToPeer: timed out waiting for a connected peer")
}

// handleManifestSync merges a received manifest into our local one.
func handleManifestSync(mosaicDir string, msg *api.Message) {
	syncData, err := msg.GetManifestSyncData()
	if err != nil {
		fmt.Println("handleManifestSync: could not parse message:", err)
		return
	}
	remote, err := filesystem.ManifestFromJSON(syncData.ManifestJSON)
	if err != nil {
		fmt.Println("handleManifestSync: could not parse remote manifest:", err)
		return
	}
	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Println("handleManifestSync: could not load network key:", err)
		return
	}
	local, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Println("handleManifestSync: could not read local manifest:", err)
		return
	}
	merged, changed := filesystem.MergeNetworkManifest(local, remote)
	if err := filesystem.WriteNetworkManifest(mosaicDir, aesKey, merged); err != nil {
		fmt.Println("handleManifestSync: could not write merged manifest:", err)
		return
	}
	fmt.Printf("handleManifestSync: merged — %d chains (changed=%v)\n", len(merged.Chains), changed)

	// If the merge brought in new information, broadcast the combined result
	// back to all peers so the network converges to the same state.
	if changed {
		go BroadcastNetworkManifest(merged)
	}

	// Replay our chain and create stubs for any files we don't have locally.
	accountID := helpers.GetAccountID()
	idx := filesystem.FindChainIndex(merged, accountID)
	if idx == -1 {
		return // no files for this user yet
	}
	files := filesystem.ChainToFiles(merged.Chains[idx])
	for _, f := range files {
		if filesystem.IsInManifest(mosaicDir, f.Name) {
			continue
		}
		if err := filesystem.AddToManifest(mosaicDir, f.Name, f.Size, accountID, f.ContentHash); err != nil {
			fmt.Printf("handleManifestSync: could not add %s to manifest: %v\n", f.Name, err)
			continue
		}
		if err := filesystem.WriteStub(mosaicDir, f.Name, f.Size, accountID, f.ContentHash); err != nil {
			fmt.Printf("handleManifestSync: could not write stub for %s: %v\n", f.Name, err)
		}
	}
	fmt.Printf("handleManifestSync: synced %d files from network\n", len(files))
}

// recordShardInManifest records that this node holds shardIndex for the file
// with contentHash, then writes and broadcasts the updated manifest.
func recordShardInManifest(contentHash string, shardIndex int) {
	mosaicDir := shared.MosaicDir()
	nodeID := fmt.Sprintf("%d", helpers.GetNodeID())

	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Printf("recordShardInManifest: could not load network key: %v\n", err)
		return
	}
	nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Printf("recordShardInManifest: could not read manifest: %v\n", err)
		return
	}
	if !filesystem.RecordShardHolder(&nm, contentHash, shardIndex, nodeID) {
		return // already recorded — nothing to write or broadcast
	}
	if err := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); err != nil {
		fmt.Printf("recordShardInManifest: could not write manifest: %v\n", err)
		return
	}
	BroadcastNetworkManifest(nm)
}

// handlePeerLeft runs when a peer is evicted after a pong timeout.
// It removes the departed peer from the ShardMap (so future fetches don't route
// to a dead node) and re-routes any locally held shards that now map to a
// different peer under the updated routing.
func handlePeerLeft(peerID string, mosaicDir string, client *p2p.Client) {
	aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
	if err != nil {
		fmt.Printf("handlePeerLeft: cannot load network key: %v\n", err)
		return
	}
	nm, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Printf("handlePeerLeft: cannot read manifest: %v\n", err)
		return
	}
	if filesystem.RemoveShardHolder(&nm, peerID) {
		if err := filesystem.WriteNetworkManifestLocked(mosaicDir, aesKey, nm); err != nil {
			fmt.Printf("handlePeerLeft: cannot write manifest: %v\n", err)
			return
		}
		BroadcastNetworkManifest(nm)
	}

	// Rebuild sorted peer ordering without the departed peer.
	ourID := client.GetID()
	connected := client.GetConnectedPeers()
	if len(connected) == 0 {
		fmt.Printf("[P2P] Last peer left — holding all shards locally\n")
		return
	}
	ids := make([]string, 0, len(connected)+1)
	ids = append(ids, ourID)
	for _, p := range connected {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	numPeers := len(ids)
	ourIndex := 0
	for i, id := range ids {
		if id == ourID {
			ourIndex = i
			break
		}
	}

	// Stream any locally held shard that now maps to a different peer.
	entries, err := os.ReadDir(transfer.ShardsDir())
	if err != nil {
		return
	}
	sent := 0
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
			shardPath := fmt.Sprintf("%s/%s/shard%d_%s.dat",
				transfer.ShardsDir(), fileHash, shardIdx, fileHash)
			if _, err := os.Stat(shardPath); err != nil {
				continue
			}
			targetIndex := shardIdx % numPeers
			if targetIndex == ourIndex {
				continue
			}
			go transfer.StreamShardToPeer(fileHash, meta, shardIdx, ids[targetIndex], client)
			sent++
		}
	}
	fmt.Printf("[P2P] Peer %s left — re-routed %d shards across %d remaining peers\n",
		peerID[:8], sent, len(connected))
}

// redistributeShardsToNewPeer scans locally stored shards and sends to newPeer
// any shard whose index maps to that peer under the routing rule:
//
//	targetPeerIndex = shardIndex % numPeers
//
// Peers are ordered by sorting all node IDs (ours + connected peers) lexicographically,
// giving a stable assignment that every node in the network can compute independently.
func redistributeShardsToNewPeer(newPeer *p2p.PeerInfo, client *p2p.Client) {
	// Build stable ordering: our ID + all current peer IDs, sorted.
	ourID := client.GetID()
	connected := client.GetConnectedPeers()
	ids := make([]string, 0, len(connected)+1)
	ids = append(ids, ourID)
	for _, p := range connected {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)

	numPeers := len(ids)
	peerIdx := -1
	for i, id := range ids {
		if id == newPeer.ID {
			peerIdx = i
			break
		}
	}
	if peerIdx == -1 {
		return
	}

	entries, err := os.ReadDir(transfer.ShardsDir())
	if err != nil {
		return
	}

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
			if shardIdx%numPeers != peerIdx {
				continue
			}
			go transfer.StreamShardToPeer(fileHash, meta, shardIdx, newPeer.ID, client)
		}
	}
	fmt.Printf("[P2P] Redistribution to peer %s complete (%d peers total)\n", newPeer.ID[:8], numPeers)
}

// handleShardResponse processes a shard received in reply to a ShardRequest.
// It stores the shard bytes locally and triggers reconstruction if enough
// data shards are now present.
func handleShardResponse(msg *api.Message) {
	d, err := msg.GetShardResponseData()
	if err != nil || !d.Found || len(d.Data) == 0 {
		return
	}

	// Look up file metadata so StoreShardData can write the meta.json.
	meta := transfer.FindShardMetaByHash(d.FileHash)
	if meta == nil {
		// No local meta yet — try to find the file entry in the network manifest.
		mosaicDir := shared.MosaicDir()
		aesKey, kerr := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath())
		if kerr != nil {
			return
		}
		nm, merr := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
		if merr != nil {
			return
		}
		for _, chain := range nm.Chains {
			for _, f := range filesystem.ChainToFiles(chain) {
				if f.ContentHash == d.FileHash {
					transfer.StoreShardData(d.FileHash, f.Name, f.Size, d.ShardIndex,
						transfer.DataShards, transfer.TotalShards, d.Data)
					return
				}
			}
		}
		fmt.Printf("handleShardResponse: no metadata found for hash %s\n", d.FileHash[:12])
		return
	}

	transfer.StoreShardData(d.FileHash, meta.FileName, meta.FileSize, d.ShardIndex,
		meta.TotalDataShards, meta.TotalShards, d.Data)
}

// announceIdentity broadcasts our account public key to all peers so they can
// record which P2P connection belongs to which account identity.
func announceIdentity(client *p2p.Client) {
	s, err := helpers.LoadSession()
	if err != nil {
		return
	}
	msg := api.NewIdentityAnnounceMessage(s.PublicKey)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.SendToAllPeers(msg); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// handleIdentityChallenge responds to an IdentityChallenge by signing the nonce
// with our private key and broadcasting the response to all peers.
func handleIdentityChallenge(msg *api.Message, client *p2p.Client) {
	d, err := msg.GetIdentityChallengeData()
	if err != nil {
		return
	}
	nonceBytes, err := hex.DecodeString(d.Nonce)
	if err != nil {
		return
	}
	kp, err := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if err != nil {
		return
	}
	h := sha256.Sum256(nonceBytes)
	sig, err := ecdsa.SignASN1(rand.Reader, kp.Private, h[:])
	if err != nil {
		return
	}
	s, err := helpers.LoadSession()
	if err != nil {
		return
	}
	resp := api.NewIdentityResponseMessage(s.PublicKey, d.Nonce, hex.EncodeToString(sig))
	client.SendToAllPeers(resp) //nolint:errcheck — best-effort
}

// parsePublicKeyHex converts a hex PKIX DER public key string to *ecdsa.PublicKey.
func parsePublicKeyHex(pubKeyHex string) (*ecdsa.PublicKey, error) {
	der, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an ECDSA public key")
	}
	return ecPub, nil
}
