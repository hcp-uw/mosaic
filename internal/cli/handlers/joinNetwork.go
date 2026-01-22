package handlers

import (
	"fmt"
	"log"
	"os/signal"
	"os"
	"syscall"
	"bufio"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/stun"
)

// Joins the network and returns a JoinResponse
func HandleJoin(req protocol.JoinRequest) protocol.JoinResponse {
	fmt.Println("Daemon: joining network.")
	// all the actual logic and stuff goes here
	// Details goes in the logs (not printed in terminal)



    runClient(req.ServerAddress)

	return protocol.JoinResponse{
		Success: true,
		Details: "Network joined successfully.",
	}
}

func runClient(serverAddr string) {
	config := stun.DefaultClientConfig(serverAddr)
	client, err := stun.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Set up callbacks
	client.OnStateChange(func(state stun.ClientState) {
		fmt.Printf("[State] %s\n", state)
	})

	client.OnPeerAssigned(func(peer *stun.PeerInfo) {
		fmt.Printf("[Peer Assigned] ID: %s, Address: %s\n", peer.ID, peer.Address)
		fmt.Println("Connecting to peer...")
		if err := client.ConnectToPeer(); err != nil {
			fmt.Printf("[Error] Failed to connect to peer: %v\n", err)
		}
	})

	client.OnError(func(err error) {
		fmt.Printf("[Error] %v\n", err)
	})

	client.OnMessageReceived(func(data []byte) {
		fmt.Printf("[Message from peer] %s\n", string(data))
	})

	// Connect to server
	fmt.Printf("Connecting to STUN server at %s...\n", serverAddr)
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Println("Connected! Waiting for peer...")
	fmt.Println("Once connected to a peer, type messages to send.")
	fmt.Println("Press Ctrl+C to disconnect.")

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Read user input and send to peer
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			if client.IsPeerCommunicationAvailable() {
				if err := client.SendToPeer([]byte(text)); err != nil {
					fmt.Printf("[Error] Failed to send message: %v\n", err)
				}
			} else {
				fmt.Println("[Info] Not connected to peer yet")
			}
		}
	}()

	<-sigChan
	fmt.Println("\nDisconnecting...")
	client.Disconnect()
}
