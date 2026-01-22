package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/stun"
)

func main() {
	port := flag.String("port", "3478", "Port to listen on (server mode)")

	runServer(*port)
}

func runServer(port string) {
	config := &stun.ServerConfig{
		ListenAddress: ":" + port,
		ClientTimeout: 30 * 1000000000, // 30 seconds in nanoseconds
		PingInterval:  10 * 1000000000, // 10 seconds in nanoseconds
		MaxQueueSize:  100,
		EnableLogging: true,
	}

	server := stun.NewServer(config)

	if err := server.Start(config); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	fmt.Printf("STUN server running on port %s\n", port)
	fmt.Println("Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down server...")
	server.Stop()
}

