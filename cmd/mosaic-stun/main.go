package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hcp-uw/mosaic/internal/stun"
)

func main() {
	port := flag.String("port", "3478", "UDP port to listen on")
	flag.Parse()

	config := &stun.ServerConfig{
		ListenAddress: ":" + *port,
		ClientTimeout: 30 * time.Second,
		PingInterval:  10 * time.Second,
		MaxQueueSize:  100,
		EnableLogging: true,
	}

	server := stun.NewServer(config)
	if err := server.Start(config); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	fmt.Printf("STUN server running on port %s\n", *port)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	server.Stop()
}
