package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hcp-uw/mosaic/internal/relay"
	"github.com/hcp-uw/mosaic/internal/stun"
)

func main() {
	port := flag.String("port", "3478", "UDP port to listen on")
	relayPort := flag.String("relay-port", "443", "TCP relay port (0 to disable); runs TLS with a self-signed cert")
	noRateLimit := flag.Bool("no-rate-limit", false, "disable per-IP registration cap (for local dev with multiple test nodes)")
	positionStore := flag.String("position-store", "/var/run/mosaic/stun-positions.json", "file for persisting queue positions across restarts (empty to disable)")
	flag.Parse()

	if os.Getenv("MOSAIC_STUN_NO_RATE_LIMIT") == "1" {
		*noRateLimit = true
	}
	if *noRateLimit {
		log.Println("Warning: per-IP rate limiting disabled (MOSAIC_STUN_NO_RATE_LIMIT or --no-rate-limit)")
	}

	config := &stun.ServerConfig{
		ListenAddress:     ":" + *port,
		ClientTimeout:     30 * time.Second,
		PingInterval:      10 * time.Second,
		MaxQueueSize:      100,
		EnableLogging:     true,
		NoRateLimit:       *noRateLimit,
		PositionStorePath: *positionStore,
	}

	server := stun.NewServer(config)
	if err := server.Start(config); err != nil {
		log.Fatalf("Failed to start STUN server: %v", err)
	}
	fmt.Printf("STUN server running on port %s\n", *port)

	if *relayPort != "0" && *relayPort != "" {
		rs := relay.NewServer(true)
		if err := rs.Start(":" + *relayPort); err != nil {
			log.Printf("Warning: TCP relay failed to start on port %s: %v", *relayPort, err)
		} else {
			fmt.Printf("TCP relay running on port %s\n", *relayPort)
			defer rs.Stop()
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	server.Stop()
}
