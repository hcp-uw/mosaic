package handlers

import (
	"fmt"
	"os"
	"sync"

	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/cli/shared"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers/helpers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/transfer"
)

// check status values.
const (
	doctorOK   = "ok"
	doctorWarn = "warn"
	doctorFail = "fail"
	doctorSkip = "skip"
)

// Doctor runs a set of self-tests and returns a per-check report. It runs inside
// the daemon so it can report the actual live connection state (e.g. which
// transport tier each peer is using) rather than only what a fresh dial reveals.
// The daemon-reachability check itself is done CLI-side — if the daemon is down,
// this handler never runs and the CLI reports that as the first failed check.
func Doctor(_ protocol.DoctorRequest) protocol.DoctorResponse {
	var checks []protocol.DoctorCheck
	add := func(name, status, detail string) {
		checks = append(checks, protocol.DoctorCheck{Name: name, Status: status, Detail: detail})
	}

	// 1. Login and derived keys.
	if !helpers.IsLoggedIn() {
		add("login", doctorWarn, "not logged in — run 'mos login <key>'")
	} else {
		add("login", doctorOK, fmt.Sprintf("logged in as %s (account %d)", helpers.GetUsername(), helpers.GetAccountID()))
	}
	checkKeyFile(add, "shard key", shared.ShardKeyPath(), 32)
	checkKeyFile(add, "signing key", shared.UserKeyPath(), 0)

	// 2. Shard storage directory is present and writable.
	shardsDir := transfer.ShardsDir()
	if err := os.MkdirAll(shardsDir, 0755); err != nil {
		add("shard storage", doctorFail, fmt.Sprintf("cannot create %s: %v", shardsDir, err))
	} else if entries, err := os.ReadDir(shardsDir); err != nil {
		add("shard storage", doctorFail, fmt.Sprintf("cannot read %s: %v", shardsDir, err))
	} else {
		fileCount := 0
		for _, e := range entries {
			if e.IsDir() {
				fileCount++
			}
		}
		add("shard storage", doctorOK, fmt.Sprintf("%s writable, %d file(s) with local shards", shardsDir, fileCount))
	}

	// 3. Network manifest decrypts.
	if aesKey, err := filesystem.LoadOrCreateNetworkKey(shared.NetworkKeyPath()); err != nil {
		add("network manifest", doctorWarn, fmt.Sprintf("cannot load network key: %v", err))
	} else if nm, err := filesystem.ReadNetworkManifest(shared.MosaicDir(), aesKey); err != nil {
		add("network manifest", doctorFail, fmt.Sprintf("manifest unreadable: %v", err))
	} else {
		add("network manifest", doctorOK, fmt.Sprintf("decrypts OK (%d user(s) known)", len(nm.Users)))
	}

	// 4 & 5. Live P2P state and per-peer transport tier.
	client := GetP2PClient()
	if client == nil {
		add("p2p connection", doctorWarn, "not joined — run 'mos join network'")
	} else {
		peers := client.GetConnectedPeers()
		add("p2p connection", doctorOK, fmt.Sprintf("state=%v, %d peer(s) connected", client.GetState(), len(peers)))
		if len(peers) == 0 {
			add("peer transport", doctorSkip, "no peers connected yet")
		} else {
			direct, relay := 0, 0
			detail := ""
			for _, p := range peers {
				tier := "direct-UDP"
				if p.ViaTURN {
					tier = "relay"
					relay++
				} else {
					direct++
				}
				detail += fmt.Sprintf("\n      %s → %s", p.ID, tier)
			}
			status := doctorOK
			if relay > 0 && direct == 0 {
				status = doctorWarn // all peers on the relay — direct UDP is not working
			}
			add("peer transport", status, fmt.Sprintf("%d direct, %d relayed%s", direct, relay, detail))
		}
	}

	// 6 & 7. Fresh reachability probes for the coordination servers. Run
	// concurrently so the slower relay dial doesn't serialize behind STUN.
	var wg sync.WaitGroup
	var stunCheck, relayCheck protocol.DoctorCheck
	wg.Add(2)
	go func() {
		defer wg.Done()
		// If we're already joined, STUN is demonstrably reachable — report the live
		// connection rather than doing a throwaway registration. An active probe
		// registers a short-lived client from our IP that the STUN roster would
		// broadcast to every peer as a phantom node (which they'd try to connect to
		// and then evict). Only probe when we're not connected.
		if client != nil && client.GetState() != p2p.StateDisconnected {
			stunCheck = protocol.DoctorCheck{Name: "STUN server", Status: doctorOK, Detail: fmt.Sprintf("%s reachable (connected, state=%v)", shared.DefaultSTUNServer, client.GetState())}
			return
		}
		if pos, err := p2p.CheckSTUNReachable(shared.DefaultSTUNServer); err != nil {
			stunCheck = protocol.DoctorCheck{Name: "STUN server", Status: doctorFail, Detail: fmt.Sprintf("%s unreachable: %v", shared.DefaultSTUNServer, err)}
		} else {
			stunCheck = protocol.DoctorCheck{Name: "STUN server", Status: doctorOK, Detail: fmt.Sprintf("%s reachable (probe queue pos %d)", shared.DefaultSTUNServer, pos)}
		}
	}()
	go func() {
		defer wg.Done()
		probeID := fmt.Sprintf("doctor-%d", helpers.GetNodeID())
		if err := p2p.CheckTCPRelayReachable(shared.DefaultTCPRelayServer, probeID); err != nil {
			relayCheck = protocol.DoctorCheck{Name: "TCP relay", Status: doctorFail, Detail: fmt.Sprintf("%s unreachable: %v — UDP-blocked networks will have no fallback", shared.DefaultTCPRelayServer, err)}
		} else {
			relayCheck = protocol.DoctorCheck{Name: "TCP relay", Status: doctorOK, Detail: fmt.Sprintf("%s reachable (TLS handshake + register/ACK OK)", shared.DefaultTCPRelayServer)}
		}
	}()
	wg.Wait()
	checks = append(checks, stunCheck, relayCheck)

	// Overall success = no hard failures.
	success := true
	for _, c := range checks {
		if c.Status == doctorFail {
			success = false
		}
	}
	return protocol.DoctorResponse{Success: success, Checks: checks}
}

// checkKeyFile adds a check for a key file's presence and (optionally) exact size.
// wantSize == 0 means "any non-empty size is fine".
func checkKeyFile(add func(name, status, detail string), name, path string, wantSize int) {
	info, err := os.Stat(path)
	if err != nil {
		add(name, doctorWarn, fmt.Sprintf("missing (%s) — log in to derive it", path))
		return
	}
	if wantSize > 0 && info.Size() != int64(wantSize) {
		add(name, doctorFail, fmt.Sprintf("corrupt: expected %d bytes, found %d", wantSize, info.Size()))
		return
	}
	add(name, doctorOK, "present")
}
