package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// LoginKey derives an ECDSA keypair from the provided key, saves it locally,
// and writes a session file. No auth server is involved.
func LoginKey(req protocol.LoginKeyRequest) protocol.LoginKeyResponse {
	fmt.Println("Daemon: logging in with key.")

	if existing, err := helpers.LoadSession(); err == nil {
		return protocol.LoginKeyResponse{
			Success:         false,
			AlreadyLoggedIn: true,
			Details:         fmt.Sprintf("already logged in (identity: %s...)", pubKeyFingerprint(existing.PublicKey)),
		}
	}

	if req.Key == "" {
		return protocol.LoginKeyResponse{Success: false, Details: "login key must not be empty"}
	}

	if err := helpers.SaveLoginKey(req.Key); err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not save login key: %v", err)}
	}

	kp, err := filesystem.DeriveUserKeyFromLoginKey(req.Key, shared.UserKeyPath())
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not derive keypair: %v", err)}
	}

	der, err := filesystem.PublicKeyBytes(kp.Public)
	if err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not serialize public key: %v", err)}
	}
	pubKeyHex := hex.EncodeToString(der)
	fp := pubKeyFingerprint(pubKeyHex)

	session := helpers.Session{PublicKey: pubKeyHex}
	if err := helpers.SaveSession(session); err != nil {
		return protocol.LoginKeyResponse{Success: false, Details: fmt.Sprintf("could not save session: %v", err)}
	}

	fmt.Printf("Daemon: logged in (identity: %s...)\n", fp)

	// Sync stubs for any files in the network manifest that aren't present locally.
	go SyncUserStubs()

	return protocol.LoginKeyResponse{
		Success: true,
		Details: fmt.Sprintf("Logged in successfully. Your identity: %s...", fp),
	}
}

// pubKeyFingerprint returns the first 8 hex chars of SHA-256(DER).
// The raw DER prefix of a P-256 PKIX public key is always "30593013...",
// so we hash the full DER to produce a unique, user-visible fingerprint.
func pubKeyFingerprint(pubKeyHex string) string {
	raw, _ := hex.DecodeString(pubKeyHex)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])[:8]
}
