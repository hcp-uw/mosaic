package handlers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"sync"
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

// ──────────────────────────────────────────────────────────
// Server public key cache
// Fetched once from /auth/pubkey/server and cached for the
// lifetime of the daemon process. All subsequent JWT verifications
// are done locally — no network calls needed.
// ──────────────────────────────────────────────────────────

var (
	cachedServerPubKey   *ecdsa.PublicKey
	cachedServerPubKeyMu sync.RWMutex
)

func getServerPublicKey() (*ecdsa.PublicKey, error) {
	cachedServerPubKeyMu.RLock()
	if cachedServerPubKey != nil {
		k := cachedServerPubKey
		cachedServerPubKeyMu.RUnlock()
		return k, nil
	}
	cachedServerPubKeyMu.RUnlock()

	resp, err := authHTTPClient.Get(authServerURL() + "/auth/pubkey/server")
	if err != nil {
		return nil, fmt.Errorf("could not reach auth server: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
		Details   string `json:"details"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("could not parse server public key response: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("auth server error: %s", result.Details)
	}

	block, _ := pem.Decode([]byte(result.PublicKey))
	if block == nil {
		return nil, fmt.Errorf("server public key response contains no PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not parse server public key: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("server public key is not ECDSA")
	}

	cachedServerPubKeyMu.Lock()
	cachedServerPubKey = ecPub
	cachedServerPubKeyMu.Unlock()

	return ecPub, nil
}

// verifyTokenLocally verifies an ES256 JWT using the server's public key.
// No network call after the first invocation — uses the cached key.
func verifyTokenLocally(token string) (*tokenClaims, error) {
	pub, err := getServerPublicKey()
	if err != nil {
		return nil, fmt.Errorf("could not get server public key: %w", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token")
	}

	unsigned := parts[0] + "." + parts[1]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("could not decode token signature: %w", err)
	}
	if len(sigBytes) != 64 {
		return nil, fmt.Errorf("invalid ES256 signature length (%d bytes)", len(sigBytes))
	}

	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])
	hash := sha256.Sum256([]byte(unsigned))

	if !ecdsa.Verify(pub, hash[:], r, s) {
		return nil, fmt.Errorf("invalid token signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("could not decode token payload: %w", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("could not parse token claims: %w", err)
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

// ──────────────────────────────────────────────────────────
// Login handler
// ──────────────────────────────────────────────────────────

type loginRequest struct {
	Username   string `json:"username"`
	LoginKey   string `json:"loginKey"`
	PublicKey  string `json:"publicKey"`
	NodeNumber int    `json:"nodeNumber"`
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

// LoginKey authenticates the user with the auth server, verifies the returned
// JWT locally against the server's public key, and writes a session file.
func LoginKey(req protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	fmt.Println("Daemon: logging in with key.")

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
		return protocol.LoginKeyResponse{Success: false, Details: "login key must not be empty"}
	}
	if req.Username == "" {
		return protocol.LoginKeyResponse{Success: false, Details: "username must not be empty"}
	}

	if err := helpers.SaveLoginKey(req.Key); err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not save login key: %v", err)}
	}

	kp, err := filesystem.DeriveUserKeyFromLoginKey(req.Key, userKeyPath())
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not derive keypair: %v", err)}
	}

	der, err := filesystem.PublicKeyBytes(kp.Public)
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not serialize public key: %v", err)}
	}
	pubKeyHex := hex.EncodeToString(der)

	existingNodeNumber := helpers.LoadNodeNumber()

	rawToken, err := callAuthLogin(req.Username, req.Key, pubKeyHex, existingNodeNumber)
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("auth server error: %v", err)}
	}

	// Verify the token locally — this is the key security check.
	// We fetch the server's public key once and verify the ES256 signature.
	claims, err := verifyTokenLocally(rawToken)
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("token verification failed: %v", err)}
	}

	session := helpers.Session{
		AccountID:  claims.AccountID,
		Username:   claims.Username,
		NodeNumber: claims.NodeNumber,
		PublicKey:  claims.PublicKey,
		ExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
		Token:      rawToken,
	}
	if err := helpers.SaveSession(session); err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not save session: %v", err)}
	}

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

// callAuthLogin POSTs to /auth/login and returns the raw JWT string.
func callAuthLogin(username, loginKey, pubKeyHex string, nodeNumber int) (string, error) {
	body, _ := json.Marshal(loginRequest{
		Username:   username,
		LoginKey:   loginKey,
		PublicKey:  pubKeyHex,
		NodeNumber: nodeNumber,
	})

	resp, err := authHTTPClient.Post(authServerURL()+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("could not reach auth server: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var serverResp loginServerResponse
	if err := json.Unmarshal(bodyBytes, &serverResp); err != nil {
		return "", fmt.Errorf("could not parse auth server response: %w", err)
	}
	if !serverResp.Success {
		return "", fmt.Errorf("%s", serverResp.Details)
	}
	return serverResp.Token, nil
}
