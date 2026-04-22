// cmd/mosaic-turn — standalone Mosaic TURN relay server.
//
// This binary is completely independent of the daemon and STUN server.
// It is not integrated into the P2P stack yet — run it separately for now.
//
// Usage:
//
//	go run ./cmd/mosaic-turn [flags]
//	./mosaic-turn -public-ip 1.2.3.4
//
// Flags:
//
//	-public-ip   Public IP of this server (required for relay to work correctly)
//	-port        UDP port to listen on (default 3478, same as STUN)
//	             Use 3479 if you're running both STUN and TURN on the same machine.
//	-users       Comma-separated user:password pairs  (default "mosaic:mosaic-turn")
//	-realm       TURN realm identifier (default "mosaic")
//	-min-port    Start of ephemeral relay port range (default 49152)
//	-max-port    End of ephemeral relay port range (default 65535)
//
// Environment variables (override flags):
//
//	TURN_PUBLIC_IP   Public IP
//	TURN_PORT        UDP port
//	TURN_USERS       user:pass,user2:pass2,...
//	TURN_REALM       Realm
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/pion/turn/v4"
)

func main() {
	publicIP := flag.String("public-ip", "", "Public IP address of this server (required)")
	port := flag.Int("port", 3479, "UDP port to listen on")
	usersStr := flag.String("users", "mosaic:mosaic-turn", "Comma-separated user:password pairs")
	realm := flag.String("realm", "mosaic", "TURN realm")
	minPort := flag.Int("min-port", 49152, "Minimum relay port")
	maxPort := flag.Int("max-port", 65535, "Maximum relay port")
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

	// Parse user:password pairs into a lookup map.
	userMap := parseUsers(*usersStr)
	if len(userMap) == 0 {
		log.Fatal("error: no valid user:password pairs found in -users flag")
	}

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	server, err := turn.NewServer(turn.ServerConfig{
		Realm: *realm,

		// AuthHandler is called for every TURN allocation request.
		// Returns the key (HMAC-MD5 of user:realm:password) for the given user.
		AuthHandler: func(username, realm string, srcAddr net.Addr) (key []byte, ok bool) {
			password, exists := userMap[username]
			if !exists {
				return nil, false
			}
			return turn.GenerateAuthKey(username, realm, password), true
		},

		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn: udpConn,
				RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
					RelayAddress: net.ParseIP(*publicIP),
					Address:      "0.0.0.0",
					MinPort:      uint16(*minPort),
					MaxPort:      uint16(*maxPort),
				},
			},
		},
	})
	if err != nil {
		log.Fatalf("failed to start TURN server: %v", err)
	}

	fmt.Printf("Mosaic TURN server listening on %s\n", addr)
	fmt.Printf("Public IP:   %s\n", *publicIP)
	fmt.Printf("Realm:       %s\n", *realm)
	fmt.Printf("Users:       %s\n", usernames(userMap))
	fmt.Printf("Relay ports: %d-%d\n", *minPort, *maxPort)

	// Block until Ctrl+C / SIGTERM.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down TURN server...")
	server.Close()
}

// parseUsers parses "user1:pass1,user2:pass2" into a map.
func parseUsers(s string) map[string]string {
	m := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

func usernames(m map[string]string) string {
	names := make([]string, 0, len(m))
	for u := range m {
		names = append(names, u)
	}
	return strings.Join(names, ", ")
}
