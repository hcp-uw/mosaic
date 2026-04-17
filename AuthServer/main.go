package main

import (
	"fmt"
	"log"
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

	// Place all server files next to the binary so everything stays in AuthServer/.
	binDir, err := binaryDir()
	if err != nil {
		log.Fatalf("could not determine binary location: %v", err)
	}

	dbPath := os.Getenv("AUTH_DB")
	if dbPath == "" {
		dbPath = filepath.Join(binDir, "mosaic-auth.db")
	}

	secretPath := filepath.Join(binDir, ".mosaic-auth-secret")

	if err := loadOrCreateSecret(secretPath); err != nil {
		log.Fatalf("could not load server secret: %v", err)
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
	mux.HandleFunc("GET /auth/pubkey/{userID}", rateLimit("pubkey", handlePubKey))

	server := &http.Server{Addr: ":" + port, Handler: mux}

	// Graceful shutdown on Ctrl+C / SIGTERM.
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		fmt.Println("\nShutting down auth server...")
		server.Close()
	}()

	fmt.Printf("Mosaic auth server listening on port %s\n", port)
	fmt.Printf("Database: %s\n", dbPath)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

// binaryDir returns the directory containing the running executable.
func binaryDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(resolved), nil
}
