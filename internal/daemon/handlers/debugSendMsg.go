package handlers

import (
	"fmt"
	"time"

	"github.com/hcp-uw/mosaic/internal/api"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

// DebugSendMsg broadcasts a plain-text message to all connected peers using the
// same session-encrypted SendToAllPeers path used by every other control message.
// Run this on node A and watch node B's daemon log for "[DEBUG] received:" to
// confirm the P2P link and session handshake are working end-to-end.
func DebugSendMsg(req protocol.DebugSendMsgRequest) protocol.DebugSendMsgResponse {
	client := GetP2PClient()
	if client == nil {
		return protocol.DebugSendMsgResponse{
			Success: false,
			Details: "not connected to network — run 'mos join network' first",
		}
	}

	peers := client.GetConnectedPeers()
	if len(peers) == 0 {
		return protocol.DebugSendMsgResponse{
			Success: false,
			Details: "connected to STUN but no peers paired yet",
		}
	}

	msg := api.NewPeerTextMessage(req.Message, client.GetID())

	// Retry for up to 3s in case the session handshake is still completing.
	var lastErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.SendToAllPeers(msg); err == nil {
			fmt.Printf("[DEBUG] sent %q to %d peer(s)\n", req.Message, len(peers))
			return protocol.DebugSendMsgResponse{
				Success:   true,
				Details:   fmt.Sprintf("sent to %d peer(s)", len(peers)),
				PeerCount: len(peers),
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}

	return protocol.DebugSendMsgResponse{
		Success:   false,
		Details:   fmt.Sprintf("send failed after 3s: %v", lastErr),
		PeerCount: len(peers),
	}
}
