# Mosaic Identity & Node Verification

This document covers how Mosaic establishes account identity across the peer-to-peer network, and how `mos status node` detects and cryptographically verifies other nodes running under the same account key.

---

## Background: Identity in Mosaic

Mosaic has no central authentication server. Identity is derived entirely from the user's login key:

```
login key (string)
    │
    ▼
HKDF-SHA256("mosaic-user-key") → 32-byte seed
    │
    ▼
ECDSA P-256 private key  →  public key  →  AccountID (uint32 from SHA-256 of pubkey)
```

The same login key on any machine produces the same ECDSA keypair. This means:
- **Same login key → same account** — provably, without any server.
- **Multiple machines with the same key** are all the same account from the network's point of view; they share one chain in the network manifest.
- **Proving ownership** means demonstrating possession of the private key derived from that login key.

---

## Session Encryption

All peer-to-peer UDP traffic is encrypted using an **ephemeral X25519 Diffie-Hellman** key exchange that runs automatically when two peers connect. No long-lived keys touch the wire.

### Handshake Flow

```
A connects to B (via STUN hole-punch or TURN relay)
    │
    ├─ A: generate ephemeral X25519 keypair (privA, pubA)
    │    send HandshakeInit{senderID=A, pubA}  →→→  B
    │
    └─ B: generate ephemeral X25519 keypair (privB, pubB)
         send HandshakeInit{senderID=B, pubB}  →→→  A

Both sides compute independently:
    sharedSecret = X25519(myEphemeralPriv, theirEphemeralPub)
    sessionKey   = HKDF-SHA256(sharedSecret, info="mosaic-session", 32 bytes)

HandshakeDone = true  →  all subsequent messages are encrypted
```

`HandshakeInit` messages are sent in plaintext (the session key can only exist after the exchange completes). Ephemeral public keys are not secret — an observer learns nothing useful from them.

### Encrypted Frame Format

After the handshake, every message is wrapped in **AES-256-GCM**:

```
[0x02] [12-byte random nonce] [AES-256-GCM ciphertext of original message]
```

The first byte acts as a routing tag:

| First byte | Meaning |
|---|---|
| `0x7B` (`{`) | Plaintext JSON — only `HandshakeInit` during the initial exchange |
| `0x02` | Session-encrypted frame — decrypt before routing |
| `0x01` | Binary shard frame (only appears inside the decrypted envelope after `0x02` unwrap) |

Overhead: 1 (magic) + 12 (nonce) + 16 (GCM tag) = **29 bytes per message** — less than 0.1% of a 32 KB shard chunk.

### TURN Relay

When a direct UDP path cannot be established, traffic is routed through the TURN relay. The relay forwards raw UDP frames and only ever sees ciphertext — the session key is computed end-to-end between the two peers, and the relay operator cannot decrypt it.

### Code Reference

| File | Purpose |
|---|---|
| `internal/p2p/peer.go` | `sealForPeer`, `openFromPeer` — AES-256-GCM helpers; `PeerInfo.SessionKey`, `HandshakeDone` |
| `internal/p2p/client.go` | `completeHandshake` — X25519 → HKDF derivation; `processPeerMessage` — 0x02 decryption |
| `internal/api/messages.go` | `HandshakeInit` message type, `NewHandshakeInitMessage`, `GetHandshakeInitData` |
| `internal/p2p/turn.go` | `handleTURNMessages` — passes `ts.peerAddr` so decryption lookup works over relay |

---

## Identity Announcement

When a node connects to a peer, it immediately broadcasts an **IdentityAnnounce** message containing its account public key (hex PKIX DER):

```
OnPeerAssigned fires
    │
    ▼
announceIdentity(client)
    │
    ▼
NewIdentityAnnounceMessage(session.PublicKey)
    │
    ▼
SendToAllPeers  ──►  each connected peer now knows:
                         "P2P connection X belongs to account pubkey Y"
```

This announcement is **not authenticated on its own** — any peer could claim any pubkey. Authentication only happens during a challenge-response.

---

## `mos status node` — Finding Same-Account Peers

Running `mos status node` scans all currently connected peers to find any that share your account identity and verifies them cryptographically.

### Flow

```
mos status node
    │
    ▼
generate 32 random nonce bytes
register response channel for nonce
    │
    ▼
broadcast IdentityChallenge{nonce} to all peers
    │
    ├──► peer A receives challenge
    │        signs sha256(nonce) with their private key
    │        broadcasts IdentityResponse{nonce, signature, pubkey}
    │
    ├──► peer B (same account) receives challenge
    │        signs sha256(nonce) with their private key (same key → same pubkey)
    │        broadcasts IdentityResponse{nonce, signature, pubkey}
    │
    ▼ (wait up to 5 seconds)
collect all IdentityResponse messages
    │
    ▼
for each response:
    is msg.Sign.PubKey == our account pubkey?
        NO  → different account, ignore
        YES → verify ECDSA signature of sha256(nonce) under that pubkey
                  valid   → AUTHENTICATED
                  invalid → FAILED
```

### Why This Works

Every peer receives the challenge and responds with their own pubkey and signature. Only a peer that actually holds the private key for your account pubkey can produce a valid ECDSA signature. There is no way to forge it.

- **Collusion resistance**: a malicious peer who knows your public key can claim your identity in their `IdentityResponse`, but they cannot produce a valid signature without the corresponding private key.
- **Replay resistance**: a fresh random nonce is generated for every scan. A recorded response from a previous scan is tied to a different nonce and will fail verification.

### Wire Format

All three identity messages are standard `api.Message` JSON envelopes. After the session handshake completes they are wrapped in AES-256-GCM (the `0x02` frame format described in [Session Encryption](#session-encryption)) before hitting the wire, so they are never readable in transit. No new transport is needed.

| Message | `type` field | Key fields |
|---|---|---|
| `IdentityAnnounce` | `"identity_announce"` | `sign.pub_key` = account pubkey hex |
| `IdentityChallenge` | `"identity_challenge"` | `data.nonce` = 32-byte hex random |
| `IdentityResponse` | `"identity_response"` | `sign.pub_key` = responder pubkey, `data.nonce` = echoed nonce, `data.signature` = hex ECDSA-ASN1 of sha256(nonce) |

---

## What "FAILED" Means

If a same-key peer responds but signature verification fails, it means one of:

1. The response was corrupted in transit (rare over UDP, but possible).
2. A peer is claiming your account pubkey but does not hold your private key — an impersonation attempt.
3. The remote node's user key file on disk is corrupt or has been replaced.

A `FAILED` result should be treated as suspicious. The legitimate fix is to log out and log back in on the remote machine, which re-derives the correct keypair from the login key.

---

## Example Output

```
$ mos status node

Node scan complete.
- This node: alice (storage shared: 20 GB)
- Scan complete. Found 2 same-account node(s) among 3 peer(s).
- Same-account nodes found:
  • 3a9f1c2b4e67... [AUTHENTICATED]
  • 3a9f1c2b4e67... [AUTHENTICATED]
```

If no same-account nodes are found:

```
$ mos status node

Node scan complete.
- This node: alice (storage shared: 20 GB)
- Scan complete. Found 0 same-account node(s) among 3 peer(s).
- No other nodes running under this account were found.
```

---

## Relevant Code

| File | Purpose |
|---|---|
| `internal/api/messages.go` | `IdentityAnnounce`, `IdentityChallenge`, `IdentityResponse`, `HandshakeInit` message types and constructors |
| `internal/daemon/handlers/joinNetwork.go` | `announceIdentity`, `handleIdentityChallenge` — send/respond to identity messages |
| `internal/daemon/handlers/p2pState.go` | `RegisterChallenge`, `DeliverChallengeResponse` — challenge channel lifecycle |
| `internal/daemon/handlers/statusNode.go` | Full scan + verification logic |
| `internal/fileSystem/userKey.go` | ECDSA keypair derivation from login key |
| `internal/p2p/peer.go` | `sealForPeer`, `openFromPeer` — AES-256-GCM session encryption; `PeerInfo` session fields |
| `internal/p2p/client.go` | `completeHandshake`, `processPeerMessage` — handshake completion and decryption |
