package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// DebugTransfer logs one diagnostic line every 500 ms for 15 seconds to the
// daemon stdout (visible in the daemon log). Each line reports:
//
//	elapsed · peer list with QUIC/TURN status · active assemblies (incoming
//	chunks being collected) · pending ACK count (outgoing shards blocked
//	waiting for ShardStreamAck) · UDP pacer interval · upload progress.
//
// Run "mos debug transfer" on the sender while an upload is in progress, then
// look for [DBG-TFR] lines in the daemon log to see exactly where time is lost.
func DebugTransfer(_ protocol.DebugTransferRequest) protocol.DebugTransferResponse {
	client := GetP2PClient()
	if client == nil {
		return protocol.DebugTransferResponse{
			Success: false,
			Details: "not connected to network — run 'mos join' first",
		}
	}

	const duration = 15 * time.Second
	const interval = 500 * time.Millisecond

	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()

	fmt.Println("[DBG-TFR] ── transfer diagnostics starting (15 s) ──")
	fmt.Println("[DBG-TFR] columns: elapsed | peers(QUIC,path) | asm=assembling | acks=pendingACK | pacer=UDPinterval | upload=done/total")

	tick := 0
	for {
		select {
		case <-deadline.C:
			fmt.Println("[DBG-TFR] ── done ──")
			return protocol.DebugTransferResponse{
				Success: true,
				Details: "15 s complete — check daemon log for [DBG-TFR] lines",
			}

		case t := <-ticker.C:
			tick++
			elapsed := t.Sub(start)
			diag := transfer.GetTransferDiagnostics()

			peers := client.GetConnectedPeers()
			peerParts := make([]string, 0, len(peers))
			for _, p := range peers {
				quic := "Q=no"
				if client.HasQUICConnection(p.ID) {
					quic = "Q=yes"
				}
				via := "direct"
				if p.ViaTURN {
					via = "TURN"
				}
				peerParts = append(peerParts, fmt.Sprintf("%s(%s,%s)", p.ID[:8], quic, via))
			}
			peersStr := strings.Join(peerParts, " ")
			if peersStr == "" {
				peersStr = "none"
			}

			fmt.Printf("[DBG-TFR] t=%5.1fs  peers=[%s]  asm=%d  acks=%d  pacer=%s  upload=%d/%d\n",
				elapsed.Seconds(),
				peersStr,
				diag.ActiveAssemblies,
				diag.PendingAcks,
				diag.UDPPacerInterval.Round(time.Millisecond),
				diag.UploadDispatched,
				diag.UploadTotal,
			)
		}
	}
}
