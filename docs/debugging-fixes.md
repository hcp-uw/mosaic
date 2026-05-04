# Debugging Session — What Was Broken and How It Was Fixed

This document covers the bugs found and fixed during the first real two-node test between a Mac client and a DigitalOcean droplet running the STUN/TURN servers.

---

## Fix 1 — Manifest Write Race Condition

### Symptom
```
handleManifestSync: could not write merged manifest: rename ...tmp ...: no such file or directory
handleManifestSync: could not read local manifest: could not decrypt network manifest: cipher: message authentication failed
```

### Root Cause
`handleManifestSync` is called concurrently — one goroutine per `ManifestSync` message received. Each goroutine independently did:

```
read manifest from disk
    ↓
merge with remote
    ↓
write .tmp file
    ↓
rename .tmp → manifest
```

When two goroutines ran simultaneously, goroutine A would write `.tmp` and rename it. Goroutine B would then try to rename a `.tmp` that no longer existed → `no such file or directory`. In the worst case, goroutine B would read the manifest mid-write by goroutine A and get a corrupt ciphertext → `cipher: message authentication failed`.

### Fix
Added `MergeAndWriteNetworkManifest` in `internal/fileSystem/networkManifest.go` that holds `networkManifestMu` for the **entire read-merge-write cycle**, not just the write. Updated `handleManifestSync` in `internal/daemon/handlers/joinNetwork.go` to call this instead of doing the three steps separately.

```go
// One mutex acquisition covers read + merge + write — no interleaving possible.
merged, changed, err := filesystem.MergeAndWriteNetworkManifest(mosaicDir, aesKey, remote)
```

---

## Fix 2 — TURN Relay Closing After 35 Seconds of Idle

### Symptom
```
[TURN] relay active for peer 178.128.151.84:3479
[P2P] Error: TURN recv error for peer ...: i/o timeout
[TURN] relay closed for peer ...
```

### Root Cause
`handleTURNMessages` in `internal/p2p/turn.go` set a 35-second read deadline before each `ReadFrom`. When no message arrived within 35 seconds (normal during idle periods between file transfers), the read returned an `i/o timeout` error which the handler treated as fatal — closing the relay and severing the connection entirely.

In environments where direct UDP hole-punching fails (e.g. Mac behind NAT connecting to a droplet), TURN is the **only** transport. Losing it means total disconnection.

### Fix
Treat read timeouts as non-fatal keepalive ticks. Only close on real errors or context cancellation:

```go
if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
    continue // idle period — reset deadline and keep looping
}
```

---

## Fix 3 — Corrupt Manifest From Prior Race

### Symptom
```
pushManifestToPeer: could not read manifest: could not decrypt network manifest: cipher: message authentication failed
```
This appeared after Fix 1 was deployed, because the manifest file on disk had already been corrupted by the race condition before the fix.

### Fix
Manual: delete the corrupt file and let it be recreated from scratch.
```bash
rm ~/Mosaic/.mosaic-network-manifest
```
On next peer connection, `pushManifestToPeer` starts from an empty manifest and the two nodes exchange and merge cleanly.

---

## Fix 4 — Graceful Leave Broadcast

### Problem
When a node disconnected gracefully (`mos leave network` or `mos logout account`), it just closed the UDP socket with no notification. Other peers waited up to **30 seconds** (pong timeout) before detecting the departure, delaying shard redistribution by 30 seconds on every graceful disconnect.

### Fix
Added `NodeLeave` message type in `internal/api/messages.go`. `DisconnectFromStun` in `internal/p2p/server_handler.go` now broadcasts `NodeLeave` to all peers before closing the socket. Receivers handle it in `processPeerMessage` by immediately evicting the peer and triggering `handlePeerLeft` — redistribution starts within milliseconds instead of 30 seconds.

---

## Confirmed Working After Fixes

- Manifest sync between two nodes (Mac + droplet) ✅
- Session encryption (X25519 handshake + AES-256-GCM) ✅
- Debug message delivery end-to-end ✅
- Shard redistribution on peer join (Mac → droplet direction) ✅
- TURN relay staying alive across idle periods ✅

## Still Under Investigation

- **Shard download from droplet**: `mos download file` returns "too few shards given". Redistribution appears to send shards Mac→droplet (7 shards logged), but the droplet cannot reconstruct. Suspected cause: the TURN fallback only triggers for peers marked `IsLeader=true`, so the droplet never establishes a TURN relay for the Mac (which is the non-leader member). ShardRequests from the droplet to the Mac go via direct UDP which may not be reachable.
