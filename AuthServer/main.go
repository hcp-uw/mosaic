package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	port := os.Getenv("AUTH_PORT")
	if port == "" {
		port = "8081"
	}

	// dataDir: use AUTH_DATA env var, otherwise the AuthServer/ folder relative
	// to the working directory. This works for both `go run ./AuthServer/` (run
	// from repo root) and the compiled binary placed inside AuthServer/.
	dataDir := os.Getenv("AUTH_DATA")
	if dataDir == "" {
		dataDir = "AuthServer"
	}

	dbPath := os.Getenv("AUTH_DB")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "mosaic-auth.db")
	}

	signingKeyPath := filepath.Join(dataDir, ".mosaic-auth-signing.pem")

	if err := loadOrCreateSigningKey(signingKeyPath); err != nil {
		log.Fatalf("could not load server signing key: %v", err)
	}

	if err := loadDB(dbPath); err != nil {
		log.Fatalf("could not open database: %v", err)
	}

	if os.Getenv("RATE_LIMIT") == "false" {
		rateLimitEnabled = false
		fmt.Println("WARNING: rate limiting disabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", rateLimit("register", handleRegister))
	mux.HandleFunc("POST /auth/login", rateLimit("login", handleLogin))
	mux.HandleFunc("POST /auth/verify", rateLimit("verify", handleVerify))
	mux.HandleFunc("GET /auth/pubkey/server", handleServerPubKey)
	mux.HandleFunc("GET /auth/pubkey/{userID}", rateLimit("pubkey", handlePubKey))

	// Force IPv4 listener so external clients on the hotspot can connect.
	ln, err := net.Listen("tcp4", "0.0.0.0:"+port)
	if err != nil {
		log.Fatalf("could not listen on port %s: %v", port, err)
	}

	server := &http.Server{Handler: mux}

	// Graceful shutdown on Ctrl+C / SIGTERM.
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		fmt.Println("\nShutting down auth server...")
		server.Shutdown(context.Background())
	}()

	fmt.Printf("Mosaic auth server listening on port %s\n", port)
	fmt.Printf("Database: %s\n", dbPath)

	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

