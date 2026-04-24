// cmd/mosaic-turn-test — quick sanity check for TURN connectivity.
// Run from repo root:
//
//	go run ./cmd/mosaic-turn-test
//
// Tries to allocate a relay on the TURN server and prints the relay address.
// If it succeeds, TURN is working end-to-end from this machine.
package main

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/pion/turn/v4"
)

func main() {
	turnAddr := shared.DefaultTURNServer
	if len(os.Args) > 1 {
		turnAddr = os.Args[1]
	}

	fmt.Printf("Testing TURN server at %s\n", turnAddr)
	fmt.Printf("  Username: %s\n", shared.TURNUsername)
	fmt.Printf("  Password: %s\n\n", shared.TURNPassword)

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		fmt.Println("FAIL: could not open UDP socket:", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Println("Step 1: connecting to TURN server...")
	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: turnAddr,
		TURNServerAddr: turnAddr,
		Conn:           conn,
		Username:       shared.TURNUsername,
		Password:       shared.TURNPassword,
	})
	if err != nil {
		fmt.Println("FAIL: client creation:", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		fmt.Println("FAIL: listen:", err)
		os.Exit(1)
	}
	fmt.Println("  OK — connected")

	fmt.Println("Step 2: allocating relay...")
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	relayConn, err := client.Allocate()
	if err != nil {
		fmt.Println("FAIL: allocation:", err)
		os.Exit(1)
	}
	defer relayConn.Close()

	fmt.Printf("  OK — relay address: %s\n\n", relayConn.LocalAddr())
	fmt.Println("PASS: TURN server is reachable and allocating relays correctly.")
}
