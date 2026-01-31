package stun

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/p2p"
)

// Example_usage demonstrates basic usage of STUN server and client
func Example_usage() {
	// Start STUN server
	serverConfig := DefaultServerConfig()
	serverConfig.ListenAddress = "127.0.0.1:0"
	serverConfig.EnableLogging = false // Disable for cleaner example output

	server := NewServer(serverConfig)
	err := server.Start(serverConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Stop()

	// Get the actual server address
	serverAddr := server.conn.LocalAddr().String()

	// Create two clients
	client1Config := p2p.DefaultClientConfig(serverAddr)
	client1, err := p2p.NewClient(client1Config)
	if err != nil {
		log.Fatal(err)
	}

	client2Config := p2p.DefaultClientConfig(serverAddr)
	client2, err := p2p.NewClient(client2Config)
	if err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup

	// Set up client 1 callbacks
	client1.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("Client 1 state: %s\n", state)
		if state == p2p.StatePaired {
			wg.Done()
		}
	})

	client1.OnPeerAssigned(func(peerInfo *p2p.PeerInfo) {
		// Don't print for deterministic test output
		_ = peerInfo
	})

	// Set up client 2 callbacks
	client2.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("Client 2 state: %s\n", state)
		if state == p2p.StatePaired {
			wg.Done()
		}
	})

	client2.OnPeerAssigned(func(peerInfo *p2p.PeerInfo) {
		// Don't print for deterministic test output
		_ = peerInfo
	})

	wg.Add(2) // Wait for both clients to be paired

	// Connect clients to server
	err = client1.ConnectToStun()
	if err != nil {
		log.Fatal(err)
	}
	defer client1.DisconnectFromStun()

	err = client2.ConnectToStun()
	if err != nil {
		log.Fatal(err)
	}
	defer client2.DisconnectFromStun()

	// Wait for pairing with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		fmt.Println("Clients successfully paired!")
	case <-time.After(5 * time.Second):
		fmt.Println("Timeout waiting for pairing")
		return
	}

	// Now clients can connect directly to each other
	err = client1.ConnectToPeer()
	if err != nil {
		log.Fatal(err)
	}

	err = client2.ConnectToPeer()
	if err != nil {
		log.Fatal(err)
	}

	// Wait for hole punching to complete
	time.Sleep(1 * time.Second)

	// Set up message received callback for client 2
	received := make(chan string, 1)
	client2.OnMessageReceived(func(data []byte) {
		received <- string(data)
	})

	// Send message from client 1 to client 2
	message := []byte("Hello from client 1!")
	err = client1.SendToPeer(client2.GetConnectedPeers()[0].ID, message)
	if err != nil {
		log.Fatal(err)
	}

	// Wait for message to be received
	select {
	case receivedMessage := <-received:
		fmt.Printf("Client 2 received: %s\n", receivedMessage)
	case <-time.After(5 * time.Second):
		log.Fatal("Timeout waiting for message")
	}
}
