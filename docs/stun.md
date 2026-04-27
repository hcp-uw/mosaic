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

## Authentication

Every client must present a valid JWT when registering. The STUN server calls the auth server's `/auth/verify` endpoint to validate it. Clients without a valid token are rejected before any pairing happens.

```
Client → STUN:  ClientRegister { token: "<JWT>" }
STUN   → Auth:  POST /auth/verify { token: "<JWT>" }
Auth   → STUN:  200 OK  (or 401 Unauthorized)
STUN   → Client: RegisterSuccess { id, queuePosition }  (or ServerError AUTH_REQUIRED)
```

The JWT is obtained at login (`mos login account <user> <key>`) and stored in `~/.mosaic-session`. The P2P client reads it automatically when connecting.

To disable authentication (development only), start with `-auth ""`.

---

## Message Flow

```
Node A                    STUN Server                Node B
  │                           │                         │
  │── ClientRegister ─────────►│                         │
  │   { token: "<JWT>" }      │── POST /auth/verify ───►│ (Auth server)
  │                           │◄── 200 OK ──────────────│
  │◄── RegisterSuccess ────────│                         │
  │    { id, queuePosition:1 }│                         │
  │◄── AssignedAsLeader ───────│                         │
  │                           │                         │
  │  (A keeps pinging STUN every 10s as leader)         │
  │                           │◄──── ClientRegister ─────│
  │                           │      { token: "<JWT>" } │
  │                           │── POST /auth/verify ───►│ (Auth server)
  │                           │◄── 200 OK ───────────────│
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

The first node to connect and pass JWT verification is assigned as **leader** (queue position 1). Subsequent nodes are paired with the leader directly via `PeerAssignment`.

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
| Unregistered node joins network | JWT required — rejected before any pairing |
| Node claims a lower queue position to become leader | Queue positions are assigned and stored server-side; clients cannot influence them |
| Node sends fake disconnect to trigger leader change | No client-initiated leader change exists — only STUN's cleanup routine triggers election |
| Node repeatedly re-registers to reset queue position | Re-registration (same IP:port) refreshes the existing record — queue position is not re-assigned |

---

## Known Limitations

### ⚠️ STUN-restart window: malicious actor can seize leadership

**Scenario:** STUN server restarts (crash, reboot, deploy). All client records are lost. The first node to re-register gets queue position 1 and becomes leader.

**Why this matters:** If a malicious authenticated node races to re-register before the legitimate leader, it gets promoted as leader and receives all subsequent `PeerAssignment` introductions. It can then intercept file-transfer coordination messages from new joiners.

**Current mitigations:**
- JWT required — the attacker must have a valid account
- Queue positions cannot be manipulated — the attacker can only win by being first, not by cheating

**What a full fix would require:**
- Persistent queue positions: STUN stores `(accountID → queuePosition)` in a database that survives restarts
- Nodes send their account ID on re-registration so STUN can restore their original position
- This was not implemented because it requires STUN to maintain persistent state, which conflicts with the "STUN is stateless between restarts" design goal

### ⚠️ Member STUN records expire silently

Because members stop pinging STUN after pairing, their records are cleaned up by STUN's 30-second inactivity timeout. If the leader dies and a member re-registers, they will get a **new** queue position (as if they are a fresh joiner), not their original one.

**Impact:** The re-registering member might not win the election even if they had the second-lowest original queue position, because another member that re-registered earlier (or never had its record expire) may have a lower current position.

**Workaround in practice:** With small networks (2–5 nodes), this is unlikely to matter. All nodes re-register quickly, and whoever had the second-lowest original position will likely still be early in the new queue.

### ⚠️ No transport security

STUN messages are sent over plain UDP with no TLS or DTLS. The JWT token itself is transmitted in plaintext to STUN. In production, this should be wrapped in DTLS or the JWT should be hash-committed so the token cannot be replayed from a network capture.

### ⚠️ Single point of coordination

STUN is not replicated. If STUN is down for more than 30 seconds, leader re-election cannot happen (though existing peer-to-peer connections continue to work). Consider running a secondary STUN instance behind a DNS failover for production deployments.

---

## Running

```bash
# Production (auth enabled, default port)
go run ./cmd/mosaic-stun

# Custom auth server
go run ./cmd/mosaic-stun -auth http://178.128.151.84:8081

# Disable auth (development only)
go run ./cmd/mosaic-stun -auth ""

# Custom port
go run ./cmd/mosaic-stun -port 3479
```

**Flags:**

| Flag    | Default                 | Description                                  |
|---------|-------------------------|----------------------------------------------|
| `-port` | `3478`                  | UDP port to listen on                        |
| `-auth` | `http://localhost:8081` | Auth server URL. Empty string disables auth. |

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
| Unauthenticated clients rejected | ✅ JWT verified via auth server on every registration |
| Leader election manipulation | ✅ Server-assigned queue positions, STUN-driven election |
| Shard data encrypted in transit | ✅ AES-256-GCM (see transfer package) |
| STUN-restart leadership race | ⚠️ First re-registrant wins; persistent queue positions not implemented |
| Member queue position preserved across STUN restart | ⚠️ Records expire — members get new positions on re-registration |
| Transport security | ⚠️ No TLS/DTLS — JWT transmitted in plaintext over UDP |
| Metadata privacy (who talks to whom) | ⚠️ STUN server sees IP:port pairs during pairing |
