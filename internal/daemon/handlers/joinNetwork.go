package handlers

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// HandleJoin joins the network and returns a JoinResponse.
func HandleJoin(req protocol.JoinRequest) protocol.JoinResponse {
	fmt.Println("Daemon: joining network.")
	runClient(req.ServerAddress)
	return protocol.JoinResponse{
		Success: true,
		Details: "Network joined successfully.",
	}
}

func runClient(serverAddr string) {
	mosaicDir := filepath.Join(os.Getenv("HOME"), "Mosaic")

	config := p2p.DefaultClientConfig(serverAddr)
	client, err := p2p.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Expose the client to handlers so they can broadcast after writes.
	SetP2PClient(client)

	client.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("[State] %s\n", state)
	})

	client.OnPeerAssigned(func(peer *p2p.PeerInfo) {
		fmt.Printf("[Peer Assigned] ID: %s, Address: %s\n", peer.ID, peer.Address)
		fmt.Println("Connecting to peer...")

		if err := client.ConnectToPeer(peer); err != nil {
			fmt.Printf("[Error] Failed to connect to peer: %v\n", err)
			return
		}

		// Push our manifest to the newly connected peer.
		go pushManifestToPeer(mosaicDir, client)
	})

	client.OnError(func(err error) {
		fmt.Printf("[Error] %v\n", err)
	})

	client.OnMessageReceived(func(data []byte) {
		msg, err := api.DeserializeMessage(data)
		if err != nil {
			return
		}
		if msg.Type == api.ManifestSync {
			go handleManifestSync(mosaicDir, msg)
		}
	})

	fmt.Printf("Connecting to STUN server at %s...\n", serverAddr)
	if err := client.ConnectToStun(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Println("Connected! Waiting for peer...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	// Read user input and send to peer
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			if client.IsPeerCommunicationAvailable() {
				msg := api.NewPeerTextMessage(text, client.GetID())
				if err := client.SendToAllPeers(msg); err != nil {
					fmt.Printf("[Error] Failed to send message: %v\n", err)
				}
			} else {
				fmt.Println("[Info] Not connected to peer yet")
			}
		}
	}()

	<-sigChan

	fmt.Println("\nDisconnecting...")
	SetP2PClient(nil)
	client.DisconnectFromStun()
}

// pushManifestToPeer sends our local manifest to a newly connected peer.
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

	msg := api.NewManifestSyncMessage(data)

	if err := client.SendToAllPeers(msg); err != nil {
		fmt.Println("pushManifestToPeer: send error:", err)
	} else {
		fmt.Println("pushManifestToPeer: manifest pushed to peer")
	}
}

// handleManifestSync merges a received manifest into our local one.
// Tampered entries are dropped by MergeNetworkManifest before any disk write.
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

	// Log accepted entries for visibility.
	fmt.Printf("handleManifestSync: merged manifest from peer — %d user entries\n", len(merged.Entries))

	// Surface own files if we can decrypt them.
	kp, kerr := filesystem.LoadOrCreateUserKey(userKeyPath())
	if kerr == nil {
		files := filesystem.GetUserFiles(merged, helpers.GetAccountID())
		_ = files // available for future use; decryption happens in read handlers
		_ = kp
	}
}
