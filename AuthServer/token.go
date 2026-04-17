package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const tokenTTL = 30 * 24 * time.Hour // 30 days

// serverSecret is loaded once at startup and used to sign all tokens.
var serverSecret []byte

// loadOrCreateSecret loads the HMAC signing secret from path, or generates
// and saves a new 32-byte random secret if none exists.
func loadOrCreateSecret(path string) error {
	data, err := os.ReadFile(path)
	if err == nil {
		serverSecret, err = hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("could not decode server secret: %w", err)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("could not read server secret: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("could not generate server secret: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)), 0600); err != nil {
		return fmt.Errorf("could not save server secret: %w", err)
	}
	serverSecret = secret
	return nil
}

// jwtHeader is the fixed base64url-encoded JWT header for HS256.
var jwtHeader = base64url([]byte(`{"alg":"HS256","typ":"JWT"}`))

// TokenClaims are the payload fields embedded in the JWT.
type TokenClaims struct {
	AccountID  int    `json:"accountID"`
	Username   string `json:"username"`
	NodeNumber int    `json:"nodeNumber"` // 1, 2, 3... per account
	PublicKey  string `json:"publicKey"`  // hex PKIX DER
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

// IssueToken creates a signed HS256 JWT for the given account and node.
func IssueToken(acc *Account, node *Node) (string, error) {
	now := time.Now()
	claims := TokenClaims{
		AccountID:  acc.AccountID,
		Username:   acc.Username,
		NodeNumber: node.NodeNumber,
		PublicKey:  acc.PublicKey,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(tokenTTL).Unix(),
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("could not marshal token claims: %w", err)
	}

	unsigned := jwtHeader + "." + base64url(payload)
	sig := hmacSHA256([]byte(unsigned))
	return unsigned + "." + base64url(sig), nil
}

// VerifyToken parses and verifies a token issued by this server.
// Returns the claims if valid, error otherwise.
func VerifyToken(token string) (*TokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token")
	}

	unsigned := parts[0] + "." + parts[1]
	expectedSig := base64url(hmacSHA256([]byte(unsigned)))
	if expectedSig != parts[2] {
		return nil, fmt.Errorf("invalid token signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("could not decode token payload: %w", err)
	}

	var claims TokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("could not parse token claims: %w", err)
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

func hmacSHA256(data []byte) []byte {
	mac := hmac.New(sha256.New, serverSecret)
	mac.Write(data)
	return mac.Sum(nil)
}

func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
