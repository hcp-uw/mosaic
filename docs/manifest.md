# Mosaic Manifest System

This document explains the full manifest system — what it is, why it exists, how it works technically, and how every piece connects together.

> **Version note:** The network manifest was redesigned as a per-user blockchain in v2. The old v1 format (ECIES-encrypted `UserNetworkEntry` blobs) is no longer used. On first read, any v1 manifest is treated as empty and rebuilt from scratch.

---

## Why a Manifest Exists

When you upload a file to Mosaic, the actual bytes get distributed to peer nodes as shards. Your local machine may or may not have the file cached. You need a way to answer the question: *"what files do I have on the network?"* without needing the file bytes to be present locally.

The manifest is the answer. It is a metadata index — a record of what exists on the network, completely independent of whether the bytes are sitting on your disk right now.

There are two separate manifests with different scopes and different security properties:

| | Local Manifest | Network Manifest |
|---|---|---|
| **Scope** | Your files, on this node | All users, all nodes |
| **Format** | Plaintext JSON | Blockchain chains, encrypted at rest |
| **Location** | `~/Mosaic/.mosaic-manifest.json` | `~/Mosaic/.mosaic-network-manifest` |
| **Who can read it** | Anyone with disk access | Any peer (file names are visible; content is not) |
| **Tamper protection** | None (local-only) | Per-block ECDSA signatures + hash chain |
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
  → .mosaic-network-manifest       "add" block appended + broadcast to peers

Double-click stub / mos download file notes.md
  → ~/Mosaic/notes.md              created  (real file)
  → ~/Mosaic/notes.md.mosaic       deleted  (stub removed by daemon)
  → .mosaic-manifest.json          updated  (cached: true)

Delete notes.md.mosaic or notes.md from Finder
  → watcher fires DeleteFile
  → ~/Mosaic/notes.md.mosaic       deleted  (if stub existed)
  → ~/Mosaic/notes.md              deleted  (if cached copy existed)
  → .mosaic-manifest.json          entry removed
  → .mosaic-network-manifest       "remove" block appended + broadcast to peers
```

---

## Part 3: The Network Manifest (v2 — Blockchain)

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
  "version": 2,
  "updatedAt": "2026-04-27T09:15:00Z",
  "chains": [
    {
      "userID": 12304938,
      "username": "a3f9b21c",
      "publicKey": "<PKIX DER P-256, base64>",
      "blocks": [
        {
          "index": 0,
          "prevHash": "",
          "op": "add",
          "file": {
            "name": "notes.md",
            "size": 4096,
            "primaryNodeID": 10,
            "dateAdded": "04-15-2026",
            "contentHash": "a3f9c2d1e8b74f..."
          },
          "timestamp": "2026-04-15T10:00:00Z",
          "signature": "<64-byte ECDSA r||s, base64>"
        },
        {
          "index": 1,
          "prevHash": "7e2b91f4c3a05d...",
          "op": "add",
          "file": { "name": "photo.jpg", ... },
          "timestamp": "2026-04-15T10:05:00Z",
          "signature": "<64-byte ECDSA r||s, base64>"
        }
      ]
    },
    {
      "userID": 99182734,
      "username": "c7f2d893",
      "publicKey": "...",
      "blocks": [ ... ]
    }
  ]
}
```

**The chains array is always sorted ascending by `userID`.** This allows binary search — `FindChainIndex` is O(log n).

Each user has exactly one chain. The current set of files is derived by **replaying the chain** — every block is an operation (add/remove/rename), and replaying them in order gives you the current file state. This is described in detail in [manifest-blockchain.md](manifest-blockchain.md).

---

## Part 4: The Security Model

### Block Signatures (Integrity)

Every block in a chain is individually signed by the owner with their ECDSA P-256 private key. The signature covers the block's content hash (all fields except the signature itself):

```
hash = SHA-256(json(block with Signature=nil))
signature = ECDSA_Sign(private_key, hash)
```

Any peer receiving a chain can verify every block using only the `PublicKey` embedded in the `UserChain` — no private key needed. A block with an invalid signature causes the entire chain to be rejected by `ValidateChain`.

### Hash Chain (Tamper Evidence)

Each block includes `prevHash`, the SHA-256 of the previous block's content. This links every block to its predecessor:

```
block[0].prevHash = ""                    ← genesis
block[1].prevHash = SHA-256(block[0])
block[2].prevHash = SHA-256(block[1])
...
```

If any historical block is altered — even one bit — its hash changes, which invalidates the `prevHash` of every subsequent block. An attacker cannot rewrite history without invalidating the entire chain from the point of modification forward.

### User Identity

Every user's identity is their ECDSA P-256 keypair, derived deterministically from their login key using HKDF-SHA256:

```
HKDF(hash=SHA-256, ikm=loginKey, salt=nil, info="mosaic-user-key") → 32-byte seed
32-byte seed → P-256 private scalar via ecdh.P256().NewPrivateKey(seed)
```

The same login key on any machine always produces the same keypair. This means:
- A user logging in on a second machine gets the exact same private key
- They can append new blocks to their chain from any machine
- Their `userID` (a fingerprint of the public key) is consistent everywhere

The derived private key is cached at `~/.mosaic-user.key` (PEM, 0600). The public key is embedded in the `UserChain` so any peer can verify blocks without knowing the private key.

### Merge and Fork Resolution

When two peers connect, each pushes their manifest to the other. `MergeNetworkManifest` merges the two:

```
for each chain in remote.chains:
    if ValidateChain(chain) fails:
        drop it (invalid signature or broken hash link)
    
    if userID not in local:
        insert the chain (new user discovered)
    else:
        winner = pickBetterChain(local_chain, remote_chain)
        replace local with winner if different
```

`pickBetterChain` uses two tiebreakers in order:
1. **Longer chain wins** — more blocks means more operations, which reflects more history
2. **Deterministic fork resolution** — if two chains have the same length but diverge, find the first differing block and pick the chain with the lexicographically lower block hash

The second rule ensures that even a true fork (two users both offline, making different operations, then reconnecting) resolves the same way on every peer in the network. The outcome is deterministic and does not require coordination.

### What Peers Can and Cannot Do

| Action | Can a random peer do this? |
|---|---|
| Read your file list | Yes — file names and sizes are visible in the chain |
| Modify your chain | No — they don't have your private key to re-sign blocks |
| Delete your blocks | No — any truncation breaks the hash chain |
| Insert a block into the middle of your chain | No — would invalidate all subsequent prevHash links |
| Fork your chain | Yes — but the deterministic merge rules resolve it consistently |
| Spam fake blocks for a new user | No — blocks must be signed with the claimed user's key; `PublicKey` in the chain is the ground truth |

File *content* is never stored in the manifest. Only metadata (name, size, node, date, content hash) is visible. The actual bytes are distributed as encrypted shards across the network.

---

## Part 5: ContentHash and Download Integrity

### What It Is

`ContentHash` is the SHA-256 hex digest of the original file bytes at upload time. It is stored in three places:

1. `ManifestEntry.ContentHash` — in the local manifest
2. `StubMeta.ContentHash` — in the `.mosaic` stub file
3. `NetworkFileEntry.ContentHash` — in the chain block's `file` field

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

1. **On peer connect** — `joinNetwork.go` calls `pushManifestToPeer`, which reads the local network manifest and sends it wrapped in a `ManifestSync` message over UDP
2. **On manifest received** — `handleManifestSync` is called in a goroutine
3. **On any local write** (upload, delete, rename) — the handler calls `BroadcastNetworkManifest` after successfully writing

### Convergence

Because the merge is deterministic (both peers run the same `pickBetterChain` logic on the same inputs), the network converges to a single canonical state even in the presence of concurrent writes and network partitions. When the merge brings in new data (`changed == true`), the node re-broadcasts the merged result so the update propagates to all connected peers.

### The Outer AES Key

The outer AES-256-GCM layer is per-node — each node generates its own `~/.mosaic-network.key` independently. This means two nodes cannot decrypt each other's on-disk manifest file. This is fine: when a manifest is transmitted via P2P, it is sent as plain JSON (`ManifestToJSON` removes the outer envelope). The individual chain data is already protected by per-block ECDSA signatures. Each node re-wraps the received JSON with its own AES key before writing to disk.

---

## Part 7: Key Files and Their Locations

```
~/.mosaic-login.key      Raw login key string (0600); source material for all key derivation
~/.mosaic-network.key    32-byte random AES-256 key, protects the network manifest at rest
~/.mosaic-user.key       ECDSA P-256 private key (PEM), derived from login key via HKDF
~/Mosaic/
  .mosaic-manifest.json          Local manifest (plaintext JSON, human-readable)
  .mosaic-network-manifest       Network manifest (binary: nonce || AES-GCM ciphertext of chain JSON)
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
     → AES-GCM decrypt .mosaic-network-manifest → NetworkManifest{chains: [...]}
5. AppendBlockAdd(&manifest, userID, username, NetworkFileEntry{...}, kp)
     → compute prevHash = SHA-256(last block in user's chain)
     → create ChainBlock{index, prevHash, op:"add", file, timestamp}
     → sign with private key → Signature
     → append to chain
6. WriteNetworkManifestLocked(mosaicDir, aesKey, manifest)
     → AES-GCM encrypt updated manifest JSON
     → write atomically to .mosaic-network-manifest
7. BroadcastNetworkManifest
     → ManifestToJSON (outer AES removed, chain data stays plaintext)
     → SendToAllPeers via UDP
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
4. ReadNetworkManifest
5. AppendBlockRemove(&manifest, userID, "notes.md", kp)
     → create ChainBlock{op:"remove", file:{name:"notes.md"}, ...}
     → sign and append
6. WriteNetworkManifestLocked → write to disk
7. BroadcastNetworkManifest → peers update their copy
```

### Rename (`mos rename file notes.md notes2.md`)

```
1. Rename ~/Mosaic/notes.md → ~/Mosaic/notes2.md (if cached)
2. Rename ~/Mosaic/notes.md.mosaic → ~/Mosaic/notes2.md.mosaic (if stub)
3. RenameInManifest → key moves from "notes.md" to "notes2.md", Name field updated
4. ReadNetworkManifest
5. AppendBlockRename(&manifest, userID, "notes.md", "notes2.md", kp)
     → create ChainBlock{op:"rename", file:{name:"notes.md"}, newName:"notes2.md", ...}
     → sign and append
6. WriteNetworkManifestLocked → write to disk
7. BroadcastNetworkManifest → peers update their copy
```

---

## Part 9: Thread Safety

The local manifest uses a package-level `sync.Mutex` (`manifestMu`). Every exported function acquires the lock before reading or writing.

The network manifest uses a separate `sync.Mutex` (`networkManifestMu`) that is acquired inside `WriteNetworkManifestLocked`. Read operations (`ReadNetworkManifest`) do not acquire this lock — they are safe to call concurrently because the write is atomic at the OS level (tmp file + rename). The mutex only prevents two concurrent writes from racing.

The P2P broadcast in `BroadcastNetworkManifest` is best-effort: if no peer is connected, it returns immediately. A failed broadcast does not affect the correctness of the local write that just succeeded.

---

## Part 10: ShardMap — Shard Location Tracking

The network manifest contains a second top-level field alongside `chains`: the `ShardMap`. It is a G-set CRDT that records which nodes hold which shards of each file.

### Structure

```json
{
  "version": 2,
  "chains": [ ... ],
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

When two manifests are merged, `mergeShardMaps` unions the holder lists for every shard of every file. Because holders are only ever added (never removed), the result is correct regardless of the order manifests arrive. This is the G-set (grow-only set) property.

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
- **Proof of Storage** — the `Tapestry` protobuf definition exists in `internal/tapestry/` and is designed for this. It will allow the network to verify that storage nodes actually hold the shards they claim to hold.
- **File name privacy for public networks** — chain blocks currently store file names in plaintext. This is suitable for a public permissionless network but means any peer can see your file names. Per-block ECIES encryption of the file metadata field can be layered on top without changing the chain structure.
- **Chain compaction** — long-lived chains with many add/remove cycles accumulate dead blocks. A compaction step (folding the chain to a single "add" block per active file) would be useful once chain length becomes a concern.
