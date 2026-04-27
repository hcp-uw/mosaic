package handlers

import (
	"context"
	"fmt"
	"log"
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
	fmt.Printf("handleManifestSync: merged — %d user entries (changed=%v)\n", len(merged.Entries), changed)

	// If the merge brought in new information, broadcast the combined result
	// back to all peers so the network converges to the same state.
	if changed {
		go BroadcastNetworkManifest(merged)
	}

	// Decrypt our section and create stubs + local manifest entries for any
	// files we don't already have locally.
	kp, kerr := filesystem.LoadOrCreateUserKey(shared.UserKeyPath())
	if kerr != nil {
		fmt.Println("handleManifestSync: could not load user key:", kerr)
		return
	}
	accountID := helpers.GetAccountID()
	idx := filesystem.FindUserIndex(merged, accountID)
	if idx == -1 {
		return // no files for this user yet
	}
	if err := filesystem.DecryptUserFiles(&merged.Entries[idx], kp.Private); err != nil {
		fmt.Println("handleManifestSync: could not decrypt user files:", err)
		return
	}
	for _, f := range merged.Entries[idx].Files {
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
	fmt.Printf("handleManifestSync: synced %d files from network\n", len(merged.Entries[idx].Files))
}
