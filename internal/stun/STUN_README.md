# Mosaic STUN Server

The STUN (Session Traversal Utilities for NAT) server is the coordination point that lets two nodes behind different NAT routers find and connect to each other directly over UDP. Once two nodes are connected peer-to-peer, the STUN server is no longer involved in their communication.

---

## What Problem It Solves

When two computers are on different networks, their routers block unsolicited incoming connections. UDP hole punching works around this:

1. Both nodes connect outward to the STUN server (both routers open an outbound hole)
2. The STUN server tells each node the other's public IP:port
3. Both nodes send a UDP packet directly to each other simultaneously — each packet arrives through the hole the other side opened
4. The connection is established

After this, the STUN server plays no further role. All file transfer, manifest sync, and ping/pong go directly between the nodes.

---

## Authentication

Every client must present a valid JWT when registering with the STUN server. The STUN server calls the auth server's `/auth/verify` endpoint to validate it. Clients without a valid token are rejected before pairing.

```
Client → STUN:  ClientRegister { token: "<JWT>" }
STUN   → Auth:  POST /auth/verify { token: "<JWT>" }
Auth   → STUN:  200 OK (or 401 Unauthorized)
STUN   → Client: RegisterSuccess (or ServerError AUTH_REQUIRED)
```

The JWT is obtained at login time (`mos login account <user> <key>`) and stored in `~/.mosaic-session`. The P2P client reads it from there when connecting.

To disable authentication (development/testing only), start the STUN server with `-auth ""`:

```bash
go run ./cmd/mosaic-stun -auth ""
```

---

## Message Flow

```
Node A                    STUN Server                Node B
  │                           │                         │
  │── ClientRegister ─────────►│                         │
  │   { token: "<JWT>" }      │── verify token ──►Auth  │
  │                           │◄── 200 OK ──────────────│
  │◄── AssignedAsLeader ───────│                         │
  │                           │                         │
  │                           │◄──── ClientRegister ─────│
  │                           │      { token: "<JWT>" } │
  │                           │── verify token ──►Auth  │
  │                           │◄── 200 OK ───────────────│
  │                           │                         │
  │◄── PeerAssignment ─────────│                         │
  │    { peerAddr, peerID }   │──── PeerAssignment ─────►│
  │                           │    { peerAddr, peerID } │
  │                           │                         │
  │── STUN_PUNCH ─────────────────────────────────────►│
  │◄─────────────────────────────────────── STUN_PUNCH ─│
  │                           │                         │
  │◄══════════ Direct UDP P2P connection established ═══►│
  │                           │                         │
  │  (STUN server no longer involved from this point)  │
```

---

## Multi-Node Networks

The first node to connect becomes the **leader**. When subsequent nodes join:

1. The STUN server tells the new node who the leader is (`PeerAssignment`)
2. The leader receives `AssignedAsLeader` and maintains a persistent connection to the STUN server
3. The leader sends the new node a `CurrentMembers` message with the full peer list
4. The leader broadcasts a `NewPeerJoiner` message to all existing peers so they can connect to the newcomer directly

The leader is responsible for introducing new nodes to the network. If the leader disconnects, the next registration triggers a new leader election (currently: first to re-register wins).

---

## Running

```bash
# Default: auth enabled pointing at localhost:8081
go run ./cmd/mosaic-stun

# Custom port
go run ./cmd/mosaic-stun -port 3478

# Custom auth server (e.g. for two-computer testing)
go run ./cmd/mosaic-stun -auth http://10.18.66.199:8081

# Disable auth (development only)
go run ./cmd/mosaic-stun -auth ""
```

**Flags:**

| Flag     | Default                  | Description                                      |
|----------|--------------------------|--------------------------------------------------|
| `-port`  | `3478`                   | UDP port to listen on                            |
| `-auth`  | `http://localhost:8081`  | Auth server URL. Empty string disables auth.     |

---

## Client Timeout

Inactive clients (no ping for 30 seconds) are removed from the server. The P2P client sends pings every 10 seconds while in the waiting/leader state to stay registered.

Once two nodes are paired they are removed from server memory — the server has no further record of them. Peer liveness is maintained by direct peer-to-peer ping/pong after that.

---

## Security Properties

| Property | Status |
|---|---|
| Unauthenticated clients rejected | ✅ JWT verified via auth server |
| Shard data encrypted in transit | ✅ AES-256-GCM (see transfer package) |
| Metadata privacy (who talks to whom) | ⚠️ STUN server sees IP:port pairs during pairing |
| STUN server itself authenticated | ⚠️ No TLS — use on trusted network or add TLS for production |
