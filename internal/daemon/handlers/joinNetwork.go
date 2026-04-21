package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// HandleJoin joins the P2P network in the background and returns immediately.
func HandleJoin(req protocol.JoinRequest) protocol.JoinResponse {
	fmt.Println("Daemon: joining network.")
	go runClient(req.ServerAddress)
	return protocol.JoinResponse{
		Success: true,
		Details: "Network joined in background.",
	}
}

func runClient(serverAddr string) {
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	config := p2p.DefaultClientConfig(serverAddr)
	config.Token = helpers.GetToken()
	client, err := p2p.NewClient(config)
	if err != nil {
		log.Printf("Failed to create P2P client: %v", err)
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
		return
	}
	fmt.Println("[P2P] Connected. Waiting for peers.")
	// runClient returns here; the P2P client goroutines keep running in background.
}

// pushManifestToPeer sends our local network manifest to a newly connected peer.
func pushManifestToPeer(mosaicDir string, client *p2p.Client) {
	aesKey, err := filesystem.LoadOrCreateNetworkKey(networkKeyPath())
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
	if err := client.SendToAllPeers(api.NewManifestSyncMessage(data)); err != nil {
		fmt.Println("pushManifestToPeer: send error:", err)
	} else {
		fmt.Println("pushManifestToPeer: manifest pushed to peer")
	}
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
	aesKey, err := filesystem.LoadOrCreateNetworkKey(networkKeyPath())
	if err != nil {
		fmt.Println("handleManifestSync: could not load network key:", err)
		return
	}
	local, err := filesystem.ReadNetworkManifest(mosaicDir, aesKey)
	if err != nil {
		fmt.Println("handleManifestSync: could not read local manifest:", err)
		return
	}
	merged := filesystem.MergeNetworkManifest(local, remote)
	if err := filesystem.WriteNetworkManifest(mosaicDir, aesKey, merged); err != nil {
		fmt.Println("handleManifestSync: could not write merged manifest:", err)
		return
	}
	fmt.Printf("handleManifestSync: merged — %d user entries\n", len(merged.Entries))

	kp, kerr := filesystem.LoadOrCreateUserKey(userKeyPath())
	if kerr == nil {
		_ = filesystem.GetUserFiles(merged, helpers.GetAccountID())
		_ = kp
	}
}
