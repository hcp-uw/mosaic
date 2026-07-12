package handlers

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
)

const identityChallengeTimeout = 5 * time.Second

// StatusNode scans the network for other nodes running under the same account key.
// For each peer that claims the same identity, it issues a cryptographic challenge
// (sign a random nonce) and verifies the response.
func StatusNode(req protocol.NodeStatusRequest) protocol.NodeStatusResponse {
	fmt.Println("Daemon: scanning network for same-account nodes.")

	client := GetP2PClient()
	if client == nil {
		return protocol.NodeStatusResponse{
			Success:  false,
			Details:  "Not connected to network — run 'mos join network' first.",
			Username: helpers.GetUsername(),
		}
	}

	s, err := helpers.LoadSession()
	if err != nil {
		return protocol.NodeStatusResponse{
			Success:  false,
			Details:  fmt.Sprintf("not logged in: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	peers := client.GetConnectedPeers()
	if len(peers) == 0 {
		return protocol.NodeStatusResponse{
			Success:      true,
			Details:      "No peers connected — cannot scan for same-account nodes.",
			Username:     helpers.GetUsername(),
			ID:           client.GetID(),
			StorageShare: helpers.StorageShare(),
			SameKeyNodes: []protocol.SameKeyNode{},
		}
	}

	// Parse our own public key for signature verification later.
	myPubKey, err := parsePublicKeyHex(s.PublicKey)
	if err != nil {
		return protocol.NodeStatusResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not parse local public key: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	// Generate a fresh 32-byte nonce for this scan.
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return protocol.NodeStatusResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not generate nonce: %v", err),
			Username: helpers.GetUsername(),
		}
	}
	nonceHex := hex.EncodeToString(nonceBytes)

	// Register channel before sending so no response can be missed.
	ch := RegisterChallenge(nonceHex)
	defer UnregisterChallenge(nonceHex)

	challenge := api.NewIdentityChallengeMessage(s.PublicKey, nonceHex)
	if err := client.SendToAllPeers(challenge); err != nil {
		return protocol.NodeStatusResponse{
			Success:  false,
			Details:  fmt.Sprintf("could not broadcast challenge: %v", err),
			Username: helpers.GetUsername(),
		}
	}

	// Collect all IdentityResponse messages that arrive within the timeout.
	// Only responses whose claimed pubkey matches ours are relevant; verify each.
	deadline := time.NewTimer(identityChallengeTimeout)
	defer deadline.Stop()

	var sameKeyNodes []protocol.SameKeyNode
	expected := len(peers) // upper bound; we stop waiting at deadline regardless

	for i := 0; i < expected; {
		select {
		case msg := <-ch:
			i++
			if msg.Sign.PubKey != s.PublicKey {
				continue // different account — ignore
			}
			d, err := msg.GetIdentityResponseData()
			if err != nil {
				sameKeyNodes = append(sameKeyNodes, protocol.SameKeyNode{
					PeerID:        msg.Sign.PubKey[:12],
					Authenticated: false,
				})
				continue
			}
			auth := verifyIdentityResponse(d, nonceBytes, myPubKey)
			sameKeyNodes = append(sameKeyNodes, protocol.SameKeyNode{
				PeerID:        s.PublicKey[:12] + "...",
				Authenticated: auth,
			})
		case <-deadline.C:
			goto done
		}
	}
done:

	details := fmt.Sprintf("Scan complete. Found %d same-account node(s) among %d peer(s).", len(sameKeyNodes), len(peers))
	return protocol.NodeStatusResponse{
		Success:      true,
		Details:      details,
		Username:     helpers.GetUsername(),
		ID:           client.GetID(),
		StorageShare: helpers.StorageShare(),
		SameKeyNodes: sameKeyNodes,
	}
}

// verifyIdentityResponse checks that the signature in d is a valid ECDSA-ASN1
// signature of sha256(nonceBytes) under pub.
func verifyIdentityResponse(d *api.IdentityResponseData, nonceBytes []byte, pub *ecdsa.PublicKey) bool {
	sigBytes, err := hex.DecodeString(d.Signature)
	if err != nil {
		return false
	}
	h := sha256.Sum256(nonceBytes)
	return ecdsa.VerifyASN1(pub, h[:], sigBytes)
}

// userKeyPath is a local alias so statusNode doesn't need the shared import.
func userKeyPath() string { return shared.UserKeyPath() }
