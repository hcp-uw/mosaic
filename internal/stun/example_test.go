package stun

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/p2p"
)

// Example_usage demonstrates basic usage of STUN server and client.
func Example_usage() {
	serverConfig := DefaultServerConfig()
	serverConfig.ListenAddress = "127.0.0.1:0"
	serverConfig.EnableLogging = false

	server := NewServer(serverConfig)
	if err := server.Start(serverConfig); err != nil {
		log.Fatal(err)
	}
	defer server.Stop()

	serverAddr := server.conn.LocalAddr().String()

	client1, err := p2p.NewClient(p2p.DefaultClientConfig(serverAddr))
	if err != nil {
		log.Fatal(err)
	}
	client2, err := p2p.NewClient(p2p.DefaultClientConfig(serverAddr))
	if err != nil {
		log.Fatal(err)
	}
	defer client1.DisconnectFromStun()
	defer client2.DisconnectFromStun()

	var wg sync.WaitGroup
	var client1Peer, client2Peer *p2p.PeerInfo

	client1.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("Client 1 state: %s\n", state)
		if state == p2p.StateLeader {
			wg.Done()
		}
	})
	client2.OnStateChange(func(state p2p.ClientState) {
		fmt.Printf("Client 2 state: %s\n", state)
		if state == p2p.StatePaired {
			wg.Done()
		}
	})

	client1.OnPeerAssigned(func(peerInfo *p2p.PeerInfo) {
		client1Peer = peerInfo
	})
	client2.OnPeerAssigned(func(peerInfo *p2p.PeerInfo) {
		client2Peer = peerInfo
	})

	wg.Add(2)

	if err := client1.ConnectToStun(); err != nil {
		log.Fatal(err)
	}
	if err := client2.ConnectToStun(); err != nil {
		log.Fatal(err)
	}

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

	if err := client1.ConnectToPeer(client1Peer); err != nil {
		log.Fatal(err)
	}
	if err := client2.ConnectToPeer(client2Peer); err != nil {
		log.Fatal(err)
	}

	time.Sleep(1 * time.Second)

	received := make(chan string, 1)
	client2.OnMessageReceived(func(data []byte) {
		received <- string(data)
	})

	message := api.NewPeerTextMessage("Hello from client 1!", client1.GetID())
	if err := client1.SendToPeer(client2Peer.ID, message); err != nil {
		log.Fatal(err)
	}

	select {
	case receivedMessage := <-received:
		fmt.Printf("Client 2 received: %s\n", receivedMessage)
	case <-time.After(5 * time.Second):
		log.Fatal("Timeout waiting for message")
	}
}
