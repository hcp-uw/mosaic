package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// authServerURL is the base URL of the Mosaic auth server.
// Override with the AUTH_SERVER environment variable.
func authServerURL() string {
	if u := os.Getenv("AUTH_SERVER"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8081"
}

type loginRequest struct {
	Username   string `json:"username"`
	LoginKey   string `json:"loginKey"`
	PublicKey  string `json:"publicKey"`
	NodeNumber int    `json:"nodeNumber"` // 0 = new device, non-zero = reuse existing
}

type loginServerResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	Details string `json:"details"`
}

type tokenClaims struct {
	AccountID  int    `json:"accountID"`
	Username   string `json:"username"`
	NodeNumber int    `json:"nodeNumber"`
	PublicKey  string `json:"publicKey"`
	ExpiresAt  int64  `json:"exp"`
}

// LoginKey authenticates the user with the auth server, derives the ECDSA
// keypair from the login key, and writes a local session file so that
// GetAccountID / GetUsername / GetNodeID return real values.
func LoginKey(req protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	fmt.Println("Daemon: logging in with key.")

	// If a valid session already exists, don't log in again.
	if existing, err := helpers.LoadSession(); err == nil {
		return protocol.LoginKeyResponse{
			Success:         false,
			AlreadyLoggedIn: true,
			Details:         fmt.Sprintf("already logged in as %s (node-%d)", existing.Username, existing.NodeNumber),
			CurrentNode:     existing.NodeNumber,
			Username:        existing.Username,
		}
	}

	if req.Key == "" {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: "login key must not be empty",
		}
	}

	if req.Username == "" {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: "username must not be empty",
		}
	}

	// Persist the raw login key so the ECDSA keypair can be re-derived later.
	if err := helpers.SaveLoginKey(req.Key); err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("could not save login key: %v", err),
		}
	}

	// Derive the deterministic ECDSA keypair from the login key.
	kp, err := filesystem.DeriveUserKeyFromLoginKey(req.Key, userKeyPath())
	if err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("could not derive keypair: %v", err),
		}
	}

	der, err := filesystem.PublicKeyBytes(kp.Public)
	if err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("could not serialize public key: %v", err),
		}
	}
	pubKeyHex := hex.EncodeToString(der)

	// Re-use the node number persisted on this machine so the same device
	// always gets the same node number, even after logout and re-login.
	existingNodeNumber := helpers.LoadNodeNumber()

	// Call the auth server.
	claims, err := callAuthLogin(req.Username, req.Key, pubKeyHex, existingNodeNumber)
	if err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("auth server error: %v", err),
		}
	}

	// Write the session file so subsequent calls to GetAccountID etc. work.
	session := helpers.Session{
		AccountID:  claims.AccountID,
		Username:   claims.Username,
		NodeNumber: claims.NodeNumber,
		PublicKey:  claims.PublicKey,
		ExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
	}
	if err := helpers.SaveSession(session); err != nil {
		return protocol.LoginKeyResponse{
			Success: false,
			Details: fmt.Sprintf("could not save session: %v", err),
		}
	}

	// Persist the node number separately so it survives logout.
	_ = helpers.SaveNodeNumber(claims.NodeNumber)

	fmt.Printf("Daemon: logged in as %s (accountID=%d node-%d)\n",
		claims.Username, claims.AccountID, claims.NodeNumber)

	return protocol.LoginKeyResponse{
		Success:     true,
		Details:     fmt.Sprintf("Logged in successfully on node-%d.", claims.NodeNumber),
		CurrentNode: claims.NodeNumber,
		Username:    claims.Username,
	}
}

// callAuthLogin POSTs to /auth/login and returns the parsed JWT claims.
func callAuthLogin(username, loginKey, pubKeyHex string, nodeNumber int) (*tokenClaims, error) {
	body, _ := json.Marshal(loginRequest{
		Username:   username,
		LoginKey:   loginKey,
		PublicKey:  pubKeyHex,
		NodeNumber: nodeNumber,
	})

	resp, err := http.Post(authServerURL()+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not reach auth server: %w", err)
	}
	defer resp.Body.Close()

	var serverResp loginServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&serverResp); err != nil {
		return nil, fmt.Errorf("could not parse auth server response: %w", err)
	}

	if !serverResp.Success {
		return nil, fmt.Errorf("%s", serverResp.Details)
	}

	claims, err := parseJWTPayload(serverResp.Token)
	if err != nil {
		return nil, fmt.Errorf("could not parse token: %w", err)
	}
	return claims, nil
}

// parseJWTPayload decodes the payload section of a JWT without verifying the
// signature — the daemon trusts the auth server's TLS connection for transport
// security. Signature verification lives in the auth server itself.
func parseJWTPayload(token string) (*tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token")
	}

	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("could not decode token payload: %w", err)
	}

	var claims tokenClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("could not parse token payload: %w", err)
	}
	return &claims, nil
}
