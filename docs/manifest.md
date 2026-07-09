# Mosaic Manifest System

This document explains the full manifest system — what it is, why it exists, how it works technically, and how every piece connects together.

> **Version note:** The network manifest has been through three designs. v1 used a single ECIES-signed snapshot. v2 used a personal hash chain (blockchain). v3 (current) uses a signed LWW-Set CRDT. v1 and v2 manifests are treated as empty on first read.

---

## Why a Manifest Exists

When you upload a file to Mosaic, the actual bytes get distributed to peer nodes as shards. Your local machine may or may not have the file cached. You need a way to answer the question: *"what files do I have on the network?"* without needing the file bytes to be present locally.

The manifest is the answer. It is a metadata index — a record of what exists on the network, completely independent of whether the bytes are sitting on your disk right now.

There are two separate manifests with different scopes and different security properties:

| | Local Manifest | Network Manifest |
|---|---|---|
| **Scope** | Your files, on this node | All users, all nodes |
| **Format** | Plaintext JSON | Signed LWW-Set CRDT, encrypted at rest |
| **Location** | `~/Mosaic/.mosaic-manifest.json` | `~/Mosaic/.mosaic-network-manifest` |
| **Who can read it** | Anyone with disk access | Any peer (only content hashes; file names AES-encrypted per-record) |
| **Tamper protection** | None (local-only) | Per-record ECDSA signatures + monotonic sequence numbers |
| **Purpose** | Fast local lookups, Finder integration | P2P sync, cross-node access, public permissionless network |

---

## Part 1: The Local Manifest

### What It Stores

The local manifest is a JSON file at `~/Mosaic/.mosaic-manifest.json`. It is a flat map from filename to a `ManifestEntry`:

```json
{
  "notes.md": {
    "name": "notes.md",
    "size": 4096,
    "nodeID": 10,
    "dateAdded": "04-15-2026",
    "cached": false,
    "contentHash": "a3f9c2d1e8b74f..."
  },
  "photo.jpg": {
    "name": "photo.jpg",
    "size": 2097152,
    "nodeID": 10,
    "dateAdded": "04-10-2026",
    "cached": true,
    "contentHash": "7e2b91f4c3a05d..."
  }
}
```

**Fields:**
- `name` — the filename (same as the map key, stored redundantly for convenience)
- `size` — original file size in bytes at time of upload
- `nodeID` — which node holds the primary shard set
- `dateAdded` — when the file was uploaded (`MM-DD-YYYY`)
- `cached` — whether the real file bytes are currently on this machine
- `contentHash` — SHA-256 hex of the original file bytes, used for integrity verification on download

### The Cached Flag vs. File Presence

`cached: true` means the file bytes exist at `~/Mosaic/<filename>`. `cached: false` means only a stub placeholder exists at `~/Mosaic/<filename>.mosaic`.

The `~/Mosaic/` folder is the source of truth for what you have locally. **Deleting anything from `~/Mosaic/` — whether a real cached file or a stub — deletes the file from the network.** The FSEvents watcher detects the deletion and calls `DeleteFile`, which removes the entry from both the local manifest and the network manifest and broadcasts the change to peers.

### How It Gets Written

Every mutation goes through the same pattern: lock → read entire map → modify in memory → write entire map → unlock.

```
manifestMu.Lock()
    entries = readManifestLocked()   // read current JSON from disk
    entries["notes.md"] = newEntry   // mutate in memory
    writeManifestLocked(entries)     // write back to disk atomically
manifestMu.Unlock()
```

**Atomic writes:** `writeManifestLocked` never writes directly to `.mosaic-manifest.json`. It writes to `.mosaic-manifest.json.tmp` first, then calls `os.Rename` to swap it in. `os.Rename` is atomic at the OS level — either the old file or the new file exists, never a half-written file.

### API

All functions in `manifest.go`:

| Function | What it does |
|---|---|
| `ReadManifest(mosaicDir)` | Returns the full map of all entries |
| `GetManifestEntry(mosaicDir, name)` | Returns a single entry, `os.ErrNotExist` if missing |
| `AddToManifest(mosaicDir, name, size, nodeID, contentHash)` | Insert or replace an entry; sets `cached: false` |
| `RemoveFromManifest(mosaicDir, name)` | Delete an entry |
| `RenameInManifest(mosaicDir, oldName, newName)` | Move an entry to a new key, preserving all fields |
| `MarkCachedInManifest(mosaicDir, name)` | Flip `cached` to `true` |
| `IsInManifest(mosaicDir, name)` | Check existence without returning the full entry |
| `RestoreManifestEntry(mosaicDir, entry)` | Re-insert a previously removed entry exactly as-is |

---

## Part 2: Stub Files

### What a Stub Is

When you upload `notes.md` and it is not being kept locally, Mosaic creates `notes.md.mosaic` in `~/Mosaic/`. This is the stub. macOS Finder shows this file with a custom badge via the FinderSync extension.

The stub is a small JSON file:

```json
{
  "name": "notes.md",
  "size": 4096,
  "nodeID": 10,
  "dateAdded": "04-15-2026",
  "cached": false,
  "contentHash": "a3f9c2d1e8b74f..."
}
```

When you double-click the stub, the Finder extension triggers a fetch. The daemon downloads the real bytes, writes them to `~/Mosaic/notes.md`, and deletes `notes.md.mosaic`. The manifest entry stays, with `cached` flipped to `true`.

### What Happens to Stubs Over Time

```
Upload notes.md (not keeping local copy)
  → ~/Mosaic/notes.md.mosaic       created  (stub)
  → .mosaic-manifest.json          updated  (cached: false)
  → .mosaic-network-manifest       RecordFileAdd + broadcast to peers

Double-click stub / mos download file notes.md
  → ~/Mosaic/notes.md              created  (real file)
  → ~/Mosaic/notes.md.mosaic       deleted  (stub removed by daemon)
  → .mosaic-manifest.json          updated  (cached: true)

Delete notes.md.mosaic or notes.md from Finder
  → watcher fires DeleteFile
  → ~/Mosaic/notes.md.mosaic       deleted  (if stub existed)
  → ~/Mosaic/notes.md              deleted  (if cached copy existed)
  → .mosaic-manifest.json          entry removed
  → .mosaic-network-manifest       RecordFileRemove (tombstone) + broadcast to peers
```

---

## Part 3: The Network Manifest (v3 — LWW-Set CRDT)

### Why It Exists

The local manifest only tracks your files on your node. When you join the network from a different machine, or when a peer needs to know what you have, the local manifest is useless — it only lives on one machine.

The network manifest is a shared, distributed index that travels across the network via P2P sync. Every node that connects to you receives a copy. It is designed for a **public, permissionless network** — any node can join and contribute, and the manifest's integrity does not depend on trusting anyone.

### File Location and Format

The network manifest lives at `~/Mosaic/.mosaic-network-manifest`. It is a binary file containing:

```
[12-byte random nonce] || [AES-256-GCM ciphertext of the full manifest JSON]
```

The outer AES-256-GCM layer protects the file at rest on disk. The key for this layer comes from `~/.mosaic-network.key`. When the manifest is sent over P2P, the outer encryption is stripped and peers receive the inner JSON; each node re-wraps it with its own AES key on receipt.

### Structure

Inside the AES-GCM envelope, the JSON looks like:

```json
{
  "version": 3,
  "updatedAt": "2026-06-07T09:15:00Z",
  "users": {
    "12304938": {
      "userID": 12304938,
      "username": "a3f9b21c",
      "publicKey": "<PKIX DER P-256, base64>",
      "records": {
        "a3f9c2d1e8b74f...": {
          "contentHash": "a3f9c2d1e8b74f...",
          "encryptedMeta": "<AES-256-GCM blob: {name, size, dateAdded}, base64>",
          "seq": 1,
          "deleted": false,
          "timestamp": "2026-06-07T10:00:00Z",
          "signature": "<64-byte ECDSA r||s, base64>"
        },
        "7e2b91f4c3a05d...": {
          "contentHash": "7e2b91f4c3a05d...",
          "encryptedMeta": "<AES-256-GCM blob>",
          "seq": 3,
          "deleted": true,
          "timestamp": "2026-06-07T11:00:00Z",
          "signature": "<64-byte ECDSA r||s, base64>"
        }
      }
    }
  },
  "shardMap": { ... }
}
```

`users` is a map from userID to `UserState`. Each `UserState` contains a map from `contentHash` to `FileRecord`. A `FileRecord` with `deleted: true` is a tombstone — the file has been removed. Tombstones are kept so that a peer who has an old version of the manifest (where the file existed) can be told authoritatively that the file is gone.

The current file set for a user is all `FileRecord`s in their map where `deleted: false`. The manifest is O(N files), not O(N operations) — it does not grow with add/remove cycles.

---

## Part 4: The Security Model

### Record Signatures (Integrity)

Every `FileRecord` is ECDSA-signed by the owner with their P-256 private key. The signature covers a deterministic canonical encoding:

```
canonical_payload =
    big_endian_uint32(userID) ||
    contentHash (ASCII)        ||
    big_endian_uint64(seq)     ||
    deleted_byte (0x00/0x01)   ||
    encryptedMeta (bytes)

signature = ECDSA_Sign(private_key, SHA-256(canonical_payload))
```

Any peer receiving a manifest can verify every record using only the `PublicKey` embedded in the `UserState` — no private key needed. A record with an invalid signature is dropped before it can be merged into the local manifest.

### Sequence Numbers (Replay Prevention)

Each `FileRecord` carries a `Seq` (sequence number) that is monotonically increasing per `(userID, contentHash)`. When a file is added (`Seq=1`), renamed (`Seq=2`), and deleted (`Seq=3`), the tombstone record has `Seq=3`. A peer that later sends `Seq=1` (the original add) is rejected — the merge rule is **strictly greater Seq only**.

This prevents an attacker from:
- Replaying a stale "add" to make a deleted file reappear
- Replaying a stale "low Seq" to roll back a rename or update

Only the record owner can produce a record with a new, higher `Seq` (because they must sign it with their private key).

### Merge Rule

`MergeNetworkManifest(local, remote)` applies LWW (Last-Write-Wins) per `(userID, contentHash)`:

```
for each (userID, contentHash) pair in remote:
    if signature verification fails → drop
    if remote.Seq > local.Seq → accept (LWW: newer wins)
    if remote.Seq == local.Seq → skip (idempotent)
    if remote.Seq < local.Seq → skip (we have newer)
```

Because only one actor (the file owner) can increment `Seq`, "newer wins" always means "the owner's most recent intent wins."

### User Identity

Every user's identity is their ECDSA P-256 keypair, derived deterministically from their login key using HKDF-SHA256:

```
HKDF(hash=SHA-256, ikm=loginKey, salt=nil, info="mosaic-user-key") → 32-byte seed
32-byte seed → P-256 private scalar via ecdh.P256().NewPrivateKey(seed)
```

The same login key on any machine always produces the same keypair. This means:
- A user logging in on a second machine gets the exact same private key
- They can record mutations (add/remove/rename) from any machine
- Their `userID` (a fingerprint of the public key) is consistent everywhere

The derived private key is cached at `~/.mosaic-user.key` (PEM, 0600). The public key is embedded in `UserState` so any peer can verify records.

### What Peers Can and Cannot Do

| Action | Can a random peer do this? |
|---|---|
| See which content hashes you have | Yes — `contentHash` travels in plaintext |
| See your file names, sizes, or dates | No — encrypted per-record with a key only you hold |
| Add a record to your user state | No — requires your private key to produce a valid signature |
| Remove a record from your user state | No — same requirement; a forged tombstone fails signature verification |
| Replay an old state to roll back your files | No — `Seq` must be strictly greater than what is already held locally |
| Flood the manifest with fake users | Theoretically yes — Sybil attack; mitigated by per-IP STUN registration cap |

File *content* is never stored in the manifest. Only `ContentHash` identifies files in the network manifest. The actual bytes are distributed as encrypted shards across the network.

---

## Part 5: ContentHash and Download Integrity

### What It Is

`ContentHash` is the SHA-256 hex digest of the original file bytes at upload time. It is stored in three places:

1. `ManifestEntry.ContentHash` — in the local manifest
2. `StubMeta.ContentHash` — in the `.mosaic` stub file
3. `FileRecord.ContentHash` — in the network manifest record

### How Verification Works

In `downloadFile.go`, after the bytes are written to disk:

```
1. Fetch bytes from network via FetchFileBytes(filename)
2. Write bytes to ~/Mosaic/<filename>
3. Look up ManifestEntry for <filename>
4. If entry.ContentHash is non-empty:
     actualHash = SHA-256(bytes just written)
     if actualHash != entry.ContentHash:
         delete ~/Mosaic/<filename>
         return failure "content hash mismatch"
5. Return success
```

If the hash check fails, the corrupted file is deleted immediately.

---

## Part 6: P2P Sync

### How the Network Manifest Travels

When a node joins the network and connects to a peer:

1. **On handshake complete** — `joinNetwork.go` calls `pushManifestToPeer`, which sends the new peer the **full** local manifest (first contact) wrapped in a `ManifestSync` message
2. **On manifest received** — `handleManifestSync` is called in a goroutine
3. **On any local write** (upload, delete, rename, shard-holder change) — the handler calls `BroadcastNetworkManifest` after successfully writing

### Delta sync

Broadcasts (step 3) are **deltas**, not full manifests. With the LWW-Set CRDT every `FileRecord` carries a monotonic `Seq`, so each node tracks — per peer, **per file** (`(userID, contentHash)`) — the highest `Seq` it believes that peer already holds (`manifestSeen` in `manifestDelta.go`) and sends only the newer records. (It must be per file, not a per-user maximum: `Seq` counts from 1 *per file*, so a newly-added file starts at Seq 1 — a per-user max would treat it as "already seen" and it would never sync.) The tracker is updated both when we *send* to a peer (optimistically) and when we *receive* from it (the peer demonstrably has what it sent us). The **ShardMap** (a G-set with no `Seq`) is sent in full in every message — only the per-user file records are delta'd.

Because the send-side update is optimistic, a delta lost over UDP would be missed, so a **periodic full-sync backstop** (`startManifestFullSync`, every 60 s) re-sends the complete manifest to every peer. CRDT merges are idempotent, so re-sending is always safe; the only risk delta sync introduces is under-sending, which the backstop heals. A departing peer's tracker is cleared, so a reconnection starts from a full sync.

### Size-adaptive transport

Manifests are sent with `SendReliableToPeer`, which picks a transport by size so the manifest can't outgrow the wire as the network scales:

- **Small (≤ ~60 KB)** → the peer's normal connection (direct UDP / TURN / TCP relay), the fast path.
- **Too big for a UDP datagram** (a UDP payload maxes at ~65 KB — roughly 50 files' worth of records + ShardMap) → **QUIC** (already established for shard transfer; reliable, ordered, no size limit), or if there's no QUIC connection, the **TCP relay** (reliable, up to 1 MB). The receive paths for both already run through `processPeerMessage`, so the message is decrypted and authenticated identically regardless of transport.

Without this, a manifest larger than one UDP datagram would silently fail to send to direct/TURN peers, and the network would stop converging. (Delta sync keeps the *record* portion small, but the full ShardMap in every message is still the growth driver — delta-ing the ShardMap by a coarse version is a future refinement.)

### Convergence

The LWW-Set merge is deterministic and commutative — both peers run the same `Seq`-wins logic on the same records and reach the same result regardless of order. When the merge brings in new data (`changed == true`), the node re-broadcasts the merged result (as a delta, excluding the sender) so the update propagates to all connected peers.

### The Outer AES Key

The outer AES-256-GCM layer is per-node — each node generates its own `~/.mosaic-network.key` independently. This means two nodes cannot decrypt each other's on-disk manifest file. When a manifest is transmitted via P2P, it is sent as plain JSON (`ManifestToJSON` removes the outer envelope). The individual records are already protected by per-record ECDSA signatures and per-record encrypted metadata. Each node re-wraps the received JSON with its own AES key before writing to disk.

---

## Part 7: Key Files and Their Locations

```
~/.mosaic-login.key      Raw login key string (0600); source material for all key derivation
~/.mosaic-network.key    32-byte random AES-256 key, protects the network manifest at rest
~/.mosaic-user.key       ECDSA P-256 private key (PEM), derived from login key via HKDF
~/Mosaic/
  .mosaic-manifest.json          Local manifest (plaintext JSON, human-readable)
  .mosaic-network-manifest       Network manifest (binary: nonce || AES-GCM ciphertext of user LWW-Set JSON)
  <filename>                     Real cached file
  <filename>.mosaic              Stub file (JSON, exists only when file is not cached locally)
```

Both key files are created with `0600` permissions — readable only by your user account.

---

## Part 8: Full Upload → Download Lifecycle

### Upload (`mos upload file notes.md`)

```
1. Read file size from disk
2. Compute SHA-256(notes.md) → contentHash
3. AddToManifest(mosaicDir, "notes.md", size, nodeID, contentHash)
     → writes to .mosaic-manifest.json atomically
4. ReadNetworkManifest(mosaicDir, aesKey)
     → AES-GCM decrypt .mosaic-network-manifest → NetworkManifest{users: {...}}
5. RecordFileAdd(&manifest, userID, username, NetworkFileEntry{...}, kp)
     → nextSeq = existing.Seq + 1 (or 1 if first)
     → encrypt {name, size, dateAdded} with metaKey → encryptedMeta
     → create FileRecord{contentHash, encryptedMeta, seq: nextSeq, deleted: false}
     → sign with private key → Signature
     → store in manifest.Users[userID].Records[contentHash]
6. WriteNetworkManifestLocked(mosaicDir, aesKey, manifest)
     → AES-GCM encrypt updated manifest JSON
     → write atomically to .mosaic-network-manifest
7. BroadcastNetworkManifest
     → ManifestToJSON (outer AES removed; per-record metadata remains AES-256-GCM encrypted)
     → SendToAllPeers
8. WriteStub(mosaicDir, "notes.md", size, nodeID, contentHash)
     → creates notes.md.mosaic
   (or MarkCachedInManifest if keeping local copy)
9. transfer.UploadFile runs concurrently:
     Reed-Solomon encode → 10 data + 4 parity shards
     Encrypt each shard (AES-256-GCM chunks) → store locally in ~/.shards/<hash>/
     Fire shardStoredCb per shard → uploader recorded in ShardMap → broadcast
     Send all 14 shards as binary frames to all connected peers
     Peers receive → finalizeShard → recorded in ShardMap → broadcast
```

### Download (double-click stub / `mos download file notes.md`)

```
1. FetchFileBytes("notes.md") from peer network
2. Write bytes to ~/Mosaic/notes.md
3. GetManifestEntry(mosaicDir, "notes.md") → entry
4. If entry.ContentHash is non-empty:
     Compute SHA-256(~/Mosaic/notes.md)
     If mismatch → delete ~/Mosaic/notes.md, return error
5. MarkCachedInManifest(mosaicDir, "notes.md") → cached: true
6. RemoveStub(mosaicDir, "notes.md") → delete notes.md.mosaic
```

### Delete (`mos delete file notes.md`)

```
1. RemoveStub (if exists)
2. Delete ~/Mosaic/notes.md (if cached)
3. RemoveFromManifest → entry gone from .mosaic-manifest.json
4. ReadNetworkManifest → get current manifest
5. FindFileByName(nm, userID, "notes.md", metaKey) → contentHash
6. RecordFileRemove(&manifest, userID, contentHash, kp)
     → nextSeq = existing.Seq + 1
     → create FileRecord{contentHash, seq: nextSeq, deleted: true}
     → sign and store (tombstone)
7. WriteNetworkManifestLocked → write to disk
8. BroadcastNetworkManifest → peers update their copy
```

### Rename (`mos rename file notes.md notes2.md`)

```
1. Rename ~/Mosaic/notes.md → ~/Mosaic/notes2.md (if cached)
2. Rename ~/Mosaic/notes.md.mosaic → ~/Mosaic/notes2.md.mosaic (if stub)
3. RenameInManifest → key moves from "notes.md" to "notes2.md", Name field updated
4. ReadNetworkManifest → get current manifest
5. FindFileByName(nm, userID, "notes.md", metaKey) → contentHash
6. RecordFileRename(&manifest, userID, contentHash, "notes2.md", kp)
     → decrypt existing encryptedMeta to get size and dateAdded
     → re-encrypt with new name
     → nextSeq = existing.Seq + 1
     → create FileRecord{contentHash, encryptedMeta: new, seq: nextSeq, deleted: false}
     → sign and store
7. WriteNetworkManifestLocked → write to disk
8. BroadcastNetworkManifest → peers update their copy
```

---

## Part 9: Thread Safety

The local manifest uses a package-level `sync.Mutex` (`manifestMu`). Every exported function acquires the lock before reading or writing.

The network manifest uses a separate `sync.Mutex` (`networkManifestMu`) that is acquired inside `WriteNetworkManifestLocked`. Read operations (`ReadNetworkManifest`) do not acquire this lock — they are safe to call concurrently because the write is atomic at the OS level (tmp file + rename). The mutex only prevents two concurrent writes from racing.

The P2P broadcast in `BroadcastNetworkManifest` is best-effort: if no peer is connected, it returns immediately. A failed broadcast does not affect the correctness of the local write that just succeeded.

---

## Part 10: ShardMap — Shard Location Tracking

The network manifest contains a second top-level field alongside `users`: the `ShardMap`. It is a G-set CRDT that records which nodes hold which shards of each file.

### Structure

```json
{
  "version": 3,
  "users": { ... },
  "shardMap": {
    "<contentHash>": {
      "holders": {
        "0": ["nodeID-A", "nodeID-B"],
        "1": ["nodeID-B"],
        "2": ["nodeID-A"],
        ...
      }
    }
  }
}
```

`shardMap` is keyed by `contentHash` (the SHA-256 of the original file). Each value maps shard indices to the list of node IDs that hold that shard.

### How It Gets Written

Every time a node stores a shard to disk — whether because it uploaded the file or received shards from a peer — it fires `shardStoredCb`, which calls `recordShardInManifest`. That function:

1. Reads the current network manifest
2. Calls `RecordShardHolder(&manifest, contentHash, shardIndex, nodeID)` — idempotent: duplicate entries are ignored
3. Writes the updated manifest to disk
4. Broadcasts the updated manifest to all peers

### CRDT Merge

When two manifests are merged, `mergeShardMaps` unions the holder lists for every shard of every file. Because holders are only ever added (never removed during normal operation), the result is correct regardless of the order manifests arrive. This is the G-set (grow-only set) property.

When a peer is evicted (pong timeout), `RemoveShardHolder` is called to clean up its entries so future fetches don't route to the dead node.

### How It Gets Used

`FetchFileBytes` uses the ShardMap to decide where to request missing shards:

```go
holders := GetShardHolders(manifest, contentHash, shardIndex)
// send ShardRequest to each holder
```

If no holders are recorded for a shard, the node cannot request it — the request is skipped and the shard must arrive via redistribution when a new peer joins.

---

## Part 11: What Is Not Yet Implemented

- **Targeted shard routing on upload** — `UploadFile` currently uses `SendRawToAllPeers`, which sends all 14 shards to every peer. The correct behaviour is `shard[i] → peers[i % numPeers]`. Redistribution on peer join already uses the correct routing rule; upload-time routing is the remaining gap.
- **Tombstone compaction** — `deleted: true` records accumulate over time as files are removed. A compaction step could prune tombstones that all peers have seen. Not yet needed in practice.
- **Key revocation** — no mechanism exists to signal that a private key has been compromised. Recovery requires abandoning the identity.
