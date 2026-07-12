// cmd/mosaic-turn — Mosaic application-layer relay server.
//
// This binary runs on the droplet alongside the STUN server and handles
// NAT traversal for peers that cannot establish direct UDP connections.
// It replaces the pion/turn TURN server with a custom relay that identifies
// peers by peer ID rather than IP:port, which correctly handles symmetric NAT.
//
// Usage:
//
//	go run ./cmd/mosaic-turn [flags]
//	./mosaic-turn -public-ip 1.2.3.4
//
// Flags:
//
//	-public-ip   Public IP of this server (required for logging; relay binds 0.0.0.0)
//	-port        UDP port to listen on (default 3479)
//	-users       Accepted for compatibility; unused by the relay protocol
//	-realm       Accepted for compatibility; unused by the relay protocol
//	-min-port    Accepted for compatibility; unused by the relay protocol
//	-max-port    Accepted for compatibility; unused by the relay protocol
//
// Environment variables (override flags):
//
//	TURN_PUBLIC_IP   Public IP
//	TURN_PORT        UDP port
//	TURN_USERS       Accepted for compatibility; unused
//	TURN_REALM       Accepted for compatibility; unused
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/turn"
)

func main() {
	publicIP := flag.String("public-ip", "", "Public IP address of this server (required)")
	port := flag.Int("port", 3479, "UDP port to listen on")
	usersStr := flag.String("users", "mosaic:mosaic-turn", "Accepted for compatibility; unused by relay protocol")
	realm := flag.String("realm", "mosaic", "Accepted for compatibility; unused by relay protocol")
	minPort := flag.Int("min-port", 49152, "Accepted for compatibility; unused by relay protocol")
	maxPort := flag.Int("max-port", 65535, "Accepted for compatibility; unused by relay protocol")
	flag.Parse()

	// Environment variables override flags.
	if v := os.Getenv("TURN_PUBLIC_IP"); v != "" {
		*publicIP = v
	}
	if v := os.Getenv("TURN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*port = n
		}
	}
	if v := os.Getenv("TURN_USERS"); v != "" {
		*usersStr = v
	}
	if v := os.Getenv("TURN_REALM"); v != "" {
		*realm = v
	}

	if *publicIP == "" {
		fmt.Fprintln(os.Stderr, "error: -public-ip is required (the public IP of this machine)")
		fmt.Fprintln(os.Stderr, "  example: ./mosaic-turn -public-ip 1.2.3.4")
		os.Exit(1)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	server, err := turn.NewServer(addr, *publicIP)
	if err != nil {
		log.Fatalf("failed to create relay server: %v", err)
	}
	server.Start()

	fmt.Printf("Mosaic relay server listening on %s\n", addr)
	fmt.Printf("Public IP:   %s\n", *publicIP)
	fmt.Printf("Realm:       %s (compatibility flag — relay protocol has no realm)\n", *realm)
	fmt.Printf("Users:       %s (compatibility flag — relay protocol has no auth)\n", usernames(*usersStr))
	fmt.Printf("Port range:  %d-%d (compatibility flags — relay protocol uses peer IDs)\n", *minPort, *maxPort)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down relay server...")
	server.Close()
}

// usernames extracts the user names from a "user:pass,user2:pass2" string for logging.
func usernames(s string) string {
	var names []string
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 && parts[0] != "" {
			names = append(names, parts[0])
		}
	}
	return strings.Join(names, ", ")
}
