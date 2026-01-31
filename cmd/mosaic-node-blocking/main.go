package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/p2p"
)

func main() {
	serverAddr := "127.0.0.1:3478"

	runClient(serverAddr)
}

func runClient(serverAddr string) {
	config := p2p.DefaultClientConfig(serverAddr)
	client, err := p2p.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Set up callbacks
	client.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("[State] %s\n", state)
	})

	client.OnPeerAssigned(func(peer *p2p.PeerInfo) {
		fmt.Printf("[Peer Assigned] ID: %s, Address: %s\n", peer.ID, peer.Address)
		fmt.Println("Connecting to peer...")

		// STUN Server sends peer Peer info and a unique ID to connect to them
		if err := client.ConnectToPeer(peer); err != nil {
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
	if err := client.ConnectToStun(); err != nil {
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
				if err := client.SendToAllPeers([]byte(text)); err != nil {
					fmt.Printf("[Error] Failed to send message: %v\n", err)
				}
			} else {
				fmt.Println("[Info] Not connected to peer yet")
			}
		}
	}()

	<-sigChan
	fmt.Println("\nDisconnecting...")
	client.DisconnectFromStun()
}
