package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

const tokenTTL = 30 * 24 * time.Hour // 30 days

// serverSigningKey is the ECDSA P-256 private key used to sign all JWTs.
// Loaded once at startup from disk (or generated fresh on first run).
var serverSigningKey *ecdsa.PrivateKey

// loadOrCreateSigningKey loads the ECDSA P-256 signing key from path,
// or generates a new one and saves it if none exists.
func loadOrCreateSigningKey(path string) error {
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return fmt.Errorf("signing key file contains no PEM block")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return fmt.Errorf("could not parse signing key: %w", err)
		}
		serverSigningKey = key
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("could not read signing key: %w", err)
	}

	// Generate a new P-256 keypair.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("could not generate signing key: %w", err)
	}

	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("could not marshal signing key: %w", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemData, 0600); err != nil {
		return fmt.Errorf("could not save signing key: %w", err)
	}

	serverSigningKey = key
	return nil
}

// ServerPublicKeyPEM returns the server's ECDSA public key as a PEM string.
// Exposed at GET /auth/pubkey/server so daemons can verify tokens locally.
func ServerPublicKeyPEM() (string, error) {
	der, err := x509.MarshalPKIXPublicKey(&serverSigningKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("could not marshal server public key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// jwtHeader is the fixed base64url-encoded JWT header for ES256.
var jwtHeader = base64url([]byte(`{"alg":"ES256","typ":"JWT"}`))

// TokenClaims are the payload fields embedded in the JWT.
type TokenClaims struct {
	AccountID  int    `json:"accountID"`
	Username   string `json:"username"`
	NodeNumber int    `json:"nodeNumber"` // 1, 2, 3... per account
	PublicKey  string `json:"publicKey"`  // hex PKIX DER of user's ECDSA key
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

// IssueToken creates a signed ES256 JWT for the given account and node.
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
	sig, err := ecdsaSign([]byte(unsigned))
	if err != nil {
		return "", fmt.Errorf("could not sign token: %w", err)
	}
	return unsigned + "." + base64url(sig), nil
}

// VerifyToken parses and verifies an ES256 JWT signed by this server.
// Returns the claims if valid, error otherwise.
func VerifyToken(token string) (*TokenClaims, error) {
	return verifyTokenWithKey(token, &serverSigningKey.PublicKey)
}

// verifyTokenWithKey verifies a JWT against a given ECDSA public key.
// Used both server-side (VerifyToken) and exported for daemon-side verification.
func verifyTokenWithKey(token string, pub *ecdsa.PublicKey) (*TokenClaims, error) {
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
		return nil, fmt.Errorf("invalid ES256 signature length (%d)", len(sigBytes))
	}

	// ES256 signature is r||s, each 32 bytes (P1363 format).
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

	var claims TokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("could not parse token claims: %w", err)
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

// ecdsaSign signs data with the server's ECDSA key.
// Returns the signature in P1363 format: r||s, each zero-padded to 32 bytes.
func ecdsaSign(data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)
	r, s, err := ecdsa.Sign(rand.Reader, serverSigningKey, hash[:])
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig, nil
}

func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
