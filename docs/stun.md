# Mosaic STUN Server

The STUN (Session Traversal Utilities for NAT) server is the coordination point that lets two nodes behind different NAT routers find and connect to each other directly over UDP. It also manages leader election and re-election when the network leader disconnects.

---

## What Problem It Solves

When two computers are on different networks, their routers block unsolicited incoming connections. UDP hole punching works around this:

1. Both nodes connect outward to the STUN server (both routers open an outbound hole)
2. The STUN server tells each node the other's public IP:port
3. Both nodes send a UDP packet directly to each other simultaneously — each packet arrives through the hole the other side opened
4. The connection is established

After pairing, all file transfer, manifest sync, and peer ping/pong go directly between the nodes. The STUN server stays in the picture only to track liveness (via leader pings) and handle leader re-election.

---

## NAT Traversal Fallback Chain

Direct hole-punching works for most NAT configurations, but fails in two specific cases: symmetric NAT (where the router assigns a different external port for each destination) and networks that block UDP entirely. Mosaic handles both with a three-tier fallback:

| Tier | Mechanism | When it activates | What it survives |
|------|-----------|-------------------|-----------------|
| 1 | **Direct UDP** (hole-punch via STUN) | Immediately on peer assignment | Most NAT types |
| 2 | **TURN relay** (UDP, port 3479) | After 25s with no pong | Symmetric NAT |
| 3 | **TCP relay** (TLS, port 443) | If TURN dial fails | Networks that block all UDP |

### Tier 1 — Direct UDP

STUN exchanges each peer's public IP:port. Both sides send UDP punch packets simultaneously to open holes in their routers, then communicate directly. The STUN server is not involved in data transfer after this point.

### Tier 2 — TURN UDP relay

If no direct pong arrives within 25 seconds, the peer that initiated the connection dials the TURN relay server. The relay identifies peers by peer ID (not IP:port), which avoids the port-mismatch that breaks standard TURN on symmetric NAT. Both peers route all traffic through the relay server; neither side needs a direct path to the other.

Once on TURN, the ping routine periodically sends hole-punch packets on the direct UDP path. If the NAT eventually opens (e.g. after a router reboot), the first direct pong promotes the peer back to direct UDP and tears down the relay connection.

### Tier 3 — TCP relay (TLS)

If the TURN dial itself fails (the network blocks all UDP, including the UDP packet to reach port 3479), the client immediately falls through to the TCP relay. The TCP relay listens on port 443 with TLS — firewalls almost universally allow outbound TCP 443, treating it as normal HTTPS traffic.

The relay payload is not inspected by the server. All peer-to-peer data is AES-256-GCM encrypted via the X25519 session key before it touches the relay, so the relay server can only see peer IDs and packet sizes, not content. `InsecureSkipVerify` is used on the TLS connection because the relay cert is self-signed — this is safe given the end-to-end encryption above it.

### Why not just always use the TCP relay?

The TCP relay is registered at STUN connection time (so the server can forward inbound messages immediately), but the peer's traffic path is only switched to TCP relay when the tiers above it fail. Direct UDP and TURN are preferred because:

- Lower latency — no relay hop
- Lower server cost — relay traffic is bandwidth the server has to pay for
- QUIC bulk transfer (shard chunks) runs over UDP; routing through a TCP relay adds framing overhead

---

## Message Flow

```
Node A                    STUN Server                Node B
  │                           │                         │
  │── ClientRegister ─────────►│                         │
  │◄── RegisterSuccess ────────│                         │
  │    { id, queuePosition:1 }│                         │
  │◄── AssignedAsLeader ───────│                         │
  │                           │                         │
  │  (A keeps pinging STUN every 10s as leader)         │
  │                           │◄──── ClientRegister ─────│
  │                           │──── RegisterSuccess ────►│
  │                           │     { id, queuePosition:2}
  │◄── PeerAssignment ─────────│                         │
  │    { peerAddr: B }        │──── PeerAssignment ─────►│
  │                           │     { peerAddr: A }     │
  │                           │                         │
  │── UDP punch ──────────────────────────────────────►│
  │◄──────────────────────────────────────── UDP punch ─│
  │                           │                         │
  │◄══════════════ Direct P2P connection established ═══►│
  │                           │                         │
  │  (A pings STUN every 10s)  │  (B pings A every 10s) │
  │── ClientPing ─────────────►│                         │
  │                           │     B stops pinging STUN │
```

---

## Liveness Model (Decentralized)

Mosaic uses a hybrid model: STUN tracks only the leader; peers track each other directly.

| Node role | Pings STUN? | Pings peers? |
|-----------|-------------|--------------|
| Leader    | Yes, every 10s | Yes, every 10s |
| Member    | No (stops after pairing) | Yes, every 10s |

**Why members stop pinging STUN:** Mosaic is a decentralized network. Once nodes are connected peer-to-peer, they should not depend on the central STUN server for liveness. STUN only needs to know the leader is alive — it uses that to drive re-election when the leader disappears.

**Peer timeout:** Any peer that doesn't pong within 30 seconds is evicted. The P2P client pings every 10 seconds, so there is a 3-missed-pings grace period before eviction.

---

## Leader Election

### Initial Assignment

The first node to connect is assigned as **leader** (queue position 1). Subsequent nodes are paired with the leader directly via `PeerAssignment`.

### Queue Positions

Every client receives a **queue position** from STUN on registration — a server-assigned integer starting at 1. Queue positions are monotonically increasing and cannot be influenced by clients. The leader always has the lowest queue position among active nodes.

### Leader Re-election: STUN-driven (leader dies while STUN is running)

All clients keep pinging the STUN server after pairing. When the cleanup routine detects the leader has stopped pinging (inactive for >30 seconds), it:

1. Removes the dead leader from its client map
2. Sorts remaining active clients by queue position (ascending)
3. Promotes the client with the **lowest queue position** as the new leader — sends `AssignedAsLeader`
4. Re-pairs all other clients with the new leader — sends them new `PeerAssignment` messages pointing to the new leader

No client votes, no `LeaderLost` messages, no consensus required. STUN is the sole authority. A client cannot self-promote.

**Example:** Nodes A(pos 1), B(pos 2), C(pos 3) are connected. A stops pinging → STUN removes A, promotes B, re-pairs C with B.

### Leader Re-election: Peer-driven (leader dies while STUN is running, from member perspective)

If a member's leader peer stops ponging (30s timeout):

1. Member evicts the dead peer locally
2. Member immediately re-registers with STUN (`ClientRegister`)
3. STUN sees the re-registration and pairs the member with whoever is now the leader (or promotes the member if it has the lowest remaining queue position)

This is a recovery path — STUN is still running and authoritative. The member simply signals "I lost my leader, please re-pair me."

### Leader Reconnects to STUN (STUN goes down and comes back)

If the leader's STUN pings fail 3 times in a row:

1. Leader marks STUN as unreachable and starts a background retry loop
2. Every 30 seconds, the leader attempts `ClientRegister` again
3. When STUN responds, the leader re-registers (STUN recognises the IP:port and refreshes the record without changing the queue position)
4. Peer-to-peer connections between leader and members remain unaffected during this outage

---

## Security Against Malicious Actors

| Threat | Mitigation |
|---|---|
| Node claims a lower queue position to become leader | Queue positions are assigned server-side; clients cannot influence them |
| Node sends fake disconnect to trigger leader change | No client-initiated leader change — only STUN's cleanup routine triggers election |
| Node repeatedly re-registers to reset queue position | Re-registration (same IP:port) refreshes the existing record without changing queue position |
| One machine flooding the network with fake identities (Sybil) | Per-IP concurrent registration cap (default 10); excess registrations are rejected with `RATE_LIMITED` |
| Client spamming STUN with pings to waste CPU | Pings arriving faster than every 5 seconds from the same client are silently dropped |
| Peer lying about holding shards it doesn't have | Shard probe challenge (nonce + SHA-256 proof); 3 consecutive failures evict the peer |
| Peer refusing to respond to shard probes | Treated the same as a wrong-hash response — counts toward the 3-strike eviction |

### Dev bypass for rate limits

Both the per-IP cap and ping rate limit are disabled when the STUN server is started with `--no-rate-limit` or `MOSAIC_STUN_NO_RATE_LIMIT=1`. Use this when running multiple test nodes on the same machine locally.

---

## Known Limitations

### ⚠️ STUN-restart window: first reconnector wins leadership

**Scenario:** STUN server restarts (crash, reboot, deploy). All client records are lost. The first node to re-register gets queue position 1 and becomes leader.

**Current mitigations:**
- Queue positions cannot be manipulated — a node can only win by being first, not by cheating

**What a full fix would require:**
- Persistent queue positions: STUN stores `(nodeID → queuePosition)` in a database that survives restarts
- Nodes send their node ID on re-registration so STUN can restore their original position
- This was not implemented because it requires STUN to maintain persistent state, which conflicts with the "STUN is stateless between restarts" design goal

### ⚠️ Member STUN records expire silently

Because members stop pinging STUN after pairing, their records are cleaned up by STUN's 30-second inactivity timeout. If the leader dies and a member re-registers, they will get a **new** queue position (as if they are a fresh joiner), not their original one.

**Impact:** The re-registering member might not win the election even if they had the second-lowest original queue position, because another member that re-registered earlier (or never had its record expire) may have a lower current position.

**Workaround in practice:** With small networks (2–5 nodes), this is unlikely to matter. All nodes re-register quickly, and whoever had the second-lowest original position will likely still be early in the new queue.

### ⚠️ No transport security

STUN messages are sent over plain UDP with no TLS or DTLS. In production, this should be wrapped in DTLS.

---

## Non-JSON Packet Filtering

Port 3478 is the well-known STUN port, so the server occasionally receives non-JSON UDP datagrams — null-byte probes, QUIC Initial packets from random scanners, and STUN/RFC-5389 binary frames from other software. The server silently drops any packet whose first byte is not `{` (0x7B) before attempting JSON deserialization. This prevents log spam from parse errors on malformed or binary payloads.

### ⚠️ Single point of coordination

STUN is not replicated. If STUN is down for more than 30 seconds, leader re-election cannot happen (though existing peer-to-peer connections continue to work). Consider running a secondary STUN instance behind a DNS failover for production deployments.

---

## Running

```bash
# Default ports (STUN on 3478, TCP relay on 443)
go run ./cmd/mosaic-stun

# Local dev with multiple test nodes on the same machine
MOSAIC_STUN_NO_RATE_LIMIT=1 go run ./cmd/mosaic-stun
# or equivalently:
go run ./cmd/mosaic-stun --no-rate-limit
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `3478` | UDP port to listen on |
| `-relay-port` | `443` | TCP relay port (TLS); `0` to disable |
| `--no-rate-limit` | off | Disable per-IP registration cap and ping rate limit (local dev) |

**Environment variables:**

| Variable | Effect |
|---|---|
| `MOSAIC_STUN_NO_RATE_LIMIT=1` | Same as `--no-rate-limit` |

---

## Client Timeout & Liveness

- Leader clients that haven't pinged STUN in **30 seconds** are removed as inactive
- Member clients are **not** tracked by STUN after pairing — they ping peers directly
- The leader pings STUN every **10 seconds** to stay registered
- All clients ping each peer every **10 seconds**; a peer that doesn't pong in **30 seconds** is evicted

---

## Security Properties

| Property | Status |
|---|---|
| Leader election manipulation | ✅ Server-assigned queue positions, STUN-driven election |
| Shard data encrypted in transit | ✅ AES-256-GCM session encryption |
| Shard integrity verification | ✅ SHA-256 content hash checked after every download |
| Shard possession proof | ✅ Nonce-based probe challenge before redistribution |
| Fake shard peer eviction | ✅ 3 consecutive probe failures → peer evicted |
| Sybil attack (one machine, many identities) | ✅ Per-IP concurrent registration cap (default 10) |
| STUN ping flood | ✅ Pings accepted at most once per 5 seconds per client |
| TCP relay connection flood | ✅ Hard cap of 200 concurrent relay connections |
| File history forgery | ✅ ECDSA-signed manifest blocks; signatures verified by peers |
| STUN-restart leadership race | ⚠️ First re-registrant wins; persistent queue positions not implemented |
| Member queue position preserved across STUN restart | ⚠️ Records expire — members get new positions on re-registration |
| P2P data transport security | ✅ AES-256-GCM session key derived via X25519 (ephemeral Diffie-Hellman); all peer messages encrypted |
| TURN relay payload security | ✅ Relay only sees AES-256-GCM ciphertext — encryption happens before the relay layer |
| TCP relay payload security | ✅ TLS transport + AES-256-GCM payload encrypted end-to-end |
| STUN control channel | ⚠️ Plain UDP — registration and ping messages to the STUN server are unencrypted JSON (no file data, only IP:port pairs) |
| Pre-handshake message authentication | ⚠️ `Sign.PubKey` is self-reported; post-handshake messages are authenticated via AES-256-GCM session key |
| Metadata privacy (who talks to whom) | ⚠️ STUN server sees IP:port pairs during pairing |
