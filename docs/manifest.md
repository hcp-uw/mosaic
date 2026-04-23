# Mosaic Manifest System

This document explains the full manifest system — what it is, why it exists, how it works technically, and how every piece connects together.

---

## Why a Manifest Exists

When you upload a file to Mosaic, the actual bytes get distributed to peer nodes as shards. Your local machine may or may not have the file cached. You need a way to answer the question: *"what files do I have on the network?"* without needing the file bytes to be present locally.

The manifest is the answer. It is a metadata index — a record of what exists on the network, completely independent of whether the bytes are sitting on your disk right now.

There are two separate manifests with different scopes and different security properties:

| | Local Manifest | Network Manifest |
|---|---|---|
| **Scope** | Your files, on this node | All users, all nodes |
| **Format** | Plaintext JSON | Encrypted binary |
| **Location** | `~/Mosaic/.mosaic-manifest.json` | `~/Mosaic/.mosaic-network-manifest` |
| **Who can read it** | Anyone with disk access | Only you (per-user encryption) |
| **Purpose** | Fast local lookups, Finder integration | P2P sync, cross-node access |

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
- `contentHash` — SHA-256 hex of the original file bytes, used for integrity verification on download; empty for files uploaded before hashing was implemented

### The Cached Flag vs. File Presence

`cached: true` means the file bytes exist at `~/Mosaic/<filename>`. `cached: false` means only a stub placeholder exists at `~/Mosaic/<filename>.mosaic`.

The `~/Mosaic/` folder is the source of truth for what you have on the network. **Deleting anything from `~/Mosaic/` — whether a real cached file or a stub — deletes the file from the network and removes it from the manifest.** The FSEvents watcher detects the deletion and calls `DeleteFile`, which removes the entry from both the local manifest and the network manifest and broadcasts the change to peers.

### How It Gets Written

Every mutation goes through the same pattern: lock → read entire map → modify in memory → write entire map → unlock.

```
manifestMu.Lock()
    entries = readManifestLocked()   // read current JSON from disk
    entries["notes.md"] = newEntry   // mutate in memory
    writeManifestLocked(entries)     // write back to disk atomically
manifestMu.Unlock()
```

**Atomic writes:** `writeManifestLocked` never writes directly to `.mosaic-manifest.json`. It writes to `.mosaic-manifest.json.tmp` first, then calls `os.Rename` to swap it in. `os.Rename` is atomic at the OS level — either the old file or the new file exists, never a half-written file. If the process crashes mid-write, the original manifest is intact.

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
| `RestoreManifestEntry(mosaicDir, entry)` | Re-insert a previously removed entry exactly as-is (used for undo) |

---

## Part 2: Stub Files

Before understanding the network manifest, it helps to understand stubs because they are how the local manifest and the file system stay in sync visually.

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
  → .mosaic-network-manifest       updated + broadcast to peers

Double-click stub / mos download file notes.md
  → ~/Mosaic/notes.md              created  (real file)
  → ~/Mosaic/notes.md.mosaic       deleted  (stub removed by daemon)
  → .mosaic-manifest.json          updated  (cached: true)

Delete notes.md.mosaic or notes.md from Finder
  → watcher fires DeleteFile
  → ~/Mosaic/notes.md.mosaic       deleted  (if stub existed)
  → ~/Mosaic/notes.md              deleted  (if cached copy existed)
  → .mosaic-manifest.json          entry removed
  → .mosaic-network-manifest       updated + broadcast to peers
```

Deleting either form of the file — the stub or the real cached copy — has the same outcome: the file is gone from the network. The `~/Mosaic/` folder mirrors the network exactly.

The stub carries `contentHash` so that integrity verification is available for remote-only files without needing to decrypt the network manifest.

---

## Part 3: The Network Manifest

### Why It Exists

The local manifest only tracks your files on your node. When you join the network from a different machine, or when a peer needs to know what you have, the local manifest is useless — it only lives on one machine.

The network manifest is a shared, distributed index that travels across the network via P2P sync. Every node that connects to you receives a copy. It contains entries for every user on the network, organized so that each user can only read their own section.

### File Location and Format

The network manifest lives at `~/Mosaic/.mosaic-network-manifest`. Unlike the local manifest, this is not human-readable JSON. It is a binary file containing:

```
[12-byte random nonce] || [AES-256-GCM ciphertext of the full manifest JSON]
```

The outer AES-256-GCM layer protects the file at rest on disk. The key for this layer comes from `~/.mosaic-network.key` — a 32-byte random key generated once per node on first run.

### Structure

Inside the AES-GCM envelope, the JSON looks like:

```json
{
  "version": 1,
  "updatedAt": "2026-04-15T14:23:01Z",
  "entries": [
    {
      "userID": 12304938,
      "username": "a3f9b21c",
      "publicKey": "<PKIX DER bytes, base64>",
      "ephemeralPubKey": "<ECDH ephemeral key bytes, base64>",
      "encryptedFiles": "<ECIES ciphertext of this user's file list, base64>",
      "signature": "<64-byte ECDSA r||s, base64>"
    },
    {
      "userID": 99182734,
      ...
    }
  ]
}
```

**The entries array is always sorted ascending by `userID`.** This is required for binary search — lookups are O(log n) using `sort.Search`.

Notice what is **not** visible: the actual list of files. That lives inside `encryptedFiles`, which only the owner of `userID: 12304938` can decrypt. A peer receiving this manifest sees opaque bytes for every user's file list.

---

## Part 4: The Two Security Layers

Each `UserNetworkEntry` has two independent security mechanisms. Understanding why both are needed requires understanding what each one protects against.

### Layer 1 — ECIES Per-User Encryption (Confidentiality)

**Problem it solves:** A malicious peer receiving the manifest should not be able to read anyone else's file list. Users also need the same keypair on every machine they log in from.

**How it works:**

ECIES stands for Elliptic Curve Integrated Encryption Scheme. The idea is to use public-key cryptography to establish a secret that is then used for symmetric encryption.

Every user has an ECDSA P-256 keypair derived deterministically from their login key. The derivation uses HKDF-SHA256:

```
HKDF(hash=SHA-256, ikm=loginKey, salt=nil, info="mosaic-user-key") → 32-byte seed
32-byte seed → P-256 private scalar → keypair
```

Because the derivation is deterministic, the same login key on any machine always produces the same keypair. A user logging in on a second machine with `mos login key <key>` gets the exact same private key, and can therefore decrypt their own manifest entries written from the first machine.

The derived private key is cached at `~/.mosaic-user.key` (PEM, 0600) so the daemon does not need the login key in memory after startup. Re-logging-in overwrites the cache with a freshly derived (identical) key. The public key is embedded in the manifest entry so any peer can verify signatures without knowing the private key.

When you write your file list to the manifest:

1. **Generate an ephemeral keypair** — a fresh, one-time-use P-256 key pair created just for this write
2. **ECDH** — perform Diffie-Hellman between the ephemeral private key and your own public key to produce a shared secret
3. **KDF** — run `SHA-256(shared_secret)` to derive a 32-byte AES key
4. **Encrypt** — use AES-256-GCM with that key to encrypt `json(Files)`
5. **Store** the ephemeral public key in `EphemeralPubKey` so you can redo the ECDH later

When you want to read your own file list back:

1. Parse `EphemeralPubKey` from the manifest entry
2. **ECDH** — perform Diffie-Hellman between your private key and the ephemeral public key (this produces the exact same shared secret because ECDH is commutative: `a·B = b·A`)
3. **KDF** — `SHA-256(shared_secret)` gives the same AES key
4. **Decrypt** AES-256-GCM → `json(Files)` → parse into `[]NetworkFileEntry`

A peer who receives this manifest has the `EphemeralPubKey` but not your private key, so they cannot compute the shared secret. Their only option is to try to break P-256 ECDH, which is computationally infeasible.

**Why encrypt to yourself?** Because you are also the recipient whenever you roam to a new node. The manifest travels across the network; anyone holding it can see the `encryptedFiles` bytes, but only the rightful owner can decrypt them.

### Layer 2 — ECDSA Ciphertext Signature (Integrity)

**Problem it solves:** A malicious peer could flip bits in someone else's `encryptedFiles` section. Even though they cannot decrypt it or produce valid new ciphertext, flipping bits could corrupt the data or cause a crash on the owner's next decrypt.

**How it works:**

After encryption, compute:
```
hash = SHA-256(EphemeralPubKey || EncryptedFiles)
```

Sign this hash with your ECDSA private key to produce a 64-byte signature `(r, s)` stored in `Signature`.

Any peer receiving the manifest can verify this signature using only the embedded `PublicKey` — no private key needed:

```
ecdsa.Verify(publicKey, SHA-256(EphemeralPubKey || EncryptedFiles), r, s)
```

If the bytes in `EncryptedFiles` or `EphemeralPubKey` have been modified, the hash changes and the signature check fails. The receiving node calls `MergeNetworkManifest`, which calls `VerifyUserEntry` on every remote entry before accepting it. **Entries that fail verification are silently dropped** — they never touch your local manifest.

**Why sign the ciphertext, not the plaintext?** Two reasons:
1. The verifier does not have your private key to decrypt, so they cannot verify a plaintext signature
2. Signing the ciphertext gives equal protection — any modification to the encrypted bytes is caught before decryption is even attempted

### Why Two Layers

These two mechanisms protect different things:

| Attack | Defeated by |
|---|---|
| Peer reads your file list | ECIES encryption |
| Peer modifies your file list bytes | ECDSA signature |
| Peer replays an old version of your entry | Timestamp + signature (old sig is valid but timestamp is stale, newer local copy wins) |
| Peer modifies another user's file list | ECDSA signature (they don't have that user's private key to re-sign) |

---

## Part 5: ContentHash and Download Integrity

### What It Is

`ContentHash` is the SHA-256 hex digest of the original file bytes at upload time. It is stored in three places:

1. `ManifestEntry.ContentHash` — in the local manifest
2. `StubMeta.ContentHash` — in the `.mosaic` stub file
3. `NetworkFileEntry.ContentHash` — inside your encrypted section of the network manifest

### Why It Matters

When you download a file, the bytes travel from a peer node across the network to your machine. Without a hash check, you have no way to know whether:
- The data was corrupted in transit
- A storage node returned wrong bytes
- A malicious actor tampered with the shard

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

If the hash check fails, the corrupted file is deleted immediately. The user sees an error. This prevents a corrupted or tampered file from silently sitting on disk passing as legitimate.

Files uploaded before ContentHash was implemented have an empty `contentHash` field and are skipped by the verification step — they pass through as before.

---

## Part 6: P2P Sync

### How the Network Manifest Travels

When a node joins the network and connects to a peer:

1. **On peer connect** — `joinNetwork.go` calls `pushManifestToPeer`, which reads the local network manifest and sends it wrapped in a `ManifestSync` message over UDP
2. **On manifest received** — the `OnMessageReceived` callback fires, `handleManifestSync` is called in a goroutine
3. **On any local write** (upload, delete, rename) — the handler calls `BroadcastNetworkManifest` after successfully writing, sending the updated manifest to all connected peers

### The Merge Algorithm

`MergeNetworkManifest(local, remote)` is called whenever a manifest arrives from a peer. It never blindly replaces the local manifest — it merges entry by entry:

```
for each entry in remote.entries:
    if VerifyUserEntry(entry) fails:
        drop it, log a warning, continue
    
    if userID not in local:
        insert the remote entry (new user discovered)
    else:
        if remote.updatedAt is strictly newer than local.updatedAt:
            replace the local entry with the remote entry
        else:
            keep the local entry
```

The result is then written atomically to disk. The key property: **a tampered entry can never enter your local manifest**, because `VerifyUserEntry` is called on every remote entry before it is accepted.

### The Outer AES Key Problem

The outer AES-256-GCM layer (the `~/.mosaic-network.key` file) is currently per-node — each node generates its own key independently. This means two different nodes cannot decrypt each other's on-disk manifest file.

However, this is not a problem for P2P sync. When the manifest is sent over the network via `ManifestToJSON`, it is serialized as plain JSON — **not** AES-encrypted. The individual user sections remain ECIES-encrypted (so file lists are still private), but the outer envelope is removed for transit. The receiving node re-wraps it in their own AES key before writing to disk.

The outer AES layer only protects the file at rest on each node's local disk.

---

## Part 7: Key Files and Their Locations

```
~/.mosaic-login.key      Raw login key string (0600); source material for all key derivation
~/.mosaic-network.key    32-byte random AES-256 key, protects the network manifest on disk
~/.mosaic-user.key       ECDSA P-256 private key (PEM), derived from login key via HKDF
~/Mosaic/
  .mosaic-manifest.json          Local manifest (plaintext JSON, human-readable)
  .mosaic-network-manifest       Network manifest (binary: nonce || AES-GCM ciphertext)
  <filename>                     Real cached file
  <filename>.mosaic              Stub file (JSON, exists only when file is not cached locally)
```

Both key files are created with `0600` permissions — readable only by your user account.

---

## Part 8: Full Upload → Download Lifecycle

Here is the complete flow for a file, showing every manifest touch point:

### Upload (`mos upload file notes.md`)

```
1. Read file size from disk
2. Compute SHA-256(notes.md) → contentHash
3. AddToManifest(mosaicDir, "notes.md", size, nodeID, contentHash)
     → writes to .mosaic-manifest.json atomically
4. ReadAndDecryptNetworkManifest
     → AES-GCM decrypt .mosaic-network-manifest
     → ECIES decrypt your UserNetworkEntry.EncryptedFiles → Files in memory
5. AddFileToNetwork(manifest, userID, username, NetworkFileEntry{...})
     → mutates Files in memory
6. EncryptSignAndWriteNetworkManifest
     → ECIES encrypt Files → EncryptedFiles
     → ECDSA sign SHA-256(EphemeralPubKey || EncryptedFiles) → Signature
     → AES-GCM encrypt full manifest JSON
     → write atomically to .mosaic-network-manifest
7. BroadcastNetworkManifest
     → ManifestToJSON (outer AES removed, ECIES sections stay)
     → SendToAllPeers via UDP
8. WriteStub(mosaicDir, "notes.md", size, nodeID, contentHash)
     → creates notes.md.mosaic
   (or MarkCachedInManifest if keeping local copy)
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
4. ReadAndDecryptNetworkManifest
5. RemoveFileFromNetwork → removes entry from Files in memory
6. EncryptSignAndWriteNetworkManifest → re-sign, re-encrypt, write
7. BroadcastNetworkManifest → peers update their copy
```

### Rename (`mos rename file notes.md notes2.md`)

```
1. Rename ~/Mosaic/notes.md → ~/Mosaic/notes2.md (if cached)
2. Rename ~/Mosaic/notes.md.mosaic → ~/Mosaic/notes2.md.mosaic (if stub)
3. RenameInManifest → key moves from "notes.md" to "notes2.md", Name field updated
4. ReadAndDecryptNetworkManifest
5. RenameFileInNetwork → mutates Name field in Files in memory
6. EncryptSignAndWriteNetworkManifest → re-sign, re-encrypt, write
7. BroadcastNetworkManifest → peers update their copy
```

---

## Part 9: Thread Safety

The local manifest uses a package-level `sync.Mutex` (`manifestMu`). Every exported function acquires the lock before reading or writing. The private `readManifestLocked` and `writeManifestLocked` functions are called only with the lock already held.

The network manifest uses a separate `sync.Mutex` (`networkManifestMu`) that is acquired inside `EncryptSignAndWriteNetworkManifest`. Read operations (`ReadNetworkManifest`, `ReadAndDecryptNetworkManifest`) do not acquire this lock — they are safe to call concurrently because the write is atomic at the OS level (tmp file + rename). The mutex only prevents two concurrent writes from racing.

The P2P broadcast in `BroadcastNetworkManifest` is best-effort: if no peer is connected, it returns immediately. If the send fails, it logs but does not propagate the error, because a failed broadcast does not affect the correctness of the local write that just succeeded.

---

## Part 10: What Is Not Yet Implemented

To give an accurate picture of the current state:

- **Shard distribution** — `FetchFileBytes` and the `TODO: distribute file shards to peers` comment in `uploadFile.go` are stubs. Files are not actually split and distributed yet. The manifest infrastructure is complete and ready; the network transport layer is the missing piece.
- **Proof of Storage** — the `Tapestry` protobuf definition exists in `internal/tapestry/` and is designed for this. It will allow the network to verify that storage nodes actually hold the shards they claim to hold, without requiring file owners to have the bytes cached locally.
- **Key distribution** — `~/.mosaic-network.key` is generated independently per node. There is no auth server. Nodes that belong to the same account share the same derived ECDSA keypair (because it is deterministically derived from the login key), but the outer AES disk-encryption key is still per-node. On-disk manifests from two different machines cannot directly decrypt each other; they exchange manifests as plain JSON over P2P and each node re-encrypts with its own AES key on receipt.
