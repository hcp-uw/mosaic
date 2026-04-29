# Mosaic Manifest Blockchain

This document explains, in depth, how the network manifest works as a personal hash chain — why it was designed this way, how every cryptographic piece fits together, and what guarantees it provides for a public permissionless network.

---

## Why a Blockchain?

Mosaic is a public network. Anyone with the CLI can join, connect to the STUN server, and receive a copy of the network manifest. This means the manifest's integrity cannot rely on trusting peers — a malicious node could receive your file list and send back a modified version to other peers.

The original v1 design used ECIES encryption (per-user private file lists) combined with ECDSA signatures over the ciphertext. This was good for privacy and tamper detection of individual snapshots, but had one structural weakness: **the entire file list was a single signed blob**. There was no way to distinguish "this user deleted a file" from "a peer dropped that file from the blob". History was invisible.

A blockchain solves this with two properties that a single signed snapshot cannot provide:

1. **Append-only history** — every operation is permanently recorded. You can always replay from genesis to verify the current state.
2. **Tamper evidence** — modifying any historical block invalidates every block that follows it, because each block's hash is a function of its predecessor.

Mosaic uses a **personal hash chain** model — not a global blockchain. Each user has their own independent chain. There is no shared mining, no global consensus, and no proof of work. The chain is simply a signed, linked log of that user's file operations. Peers merge their views by comparing chains and keeping the better one.

---

## Data Structures

### `ChainBlock`

A single operation in a user's history.

```go
type ChainBlock struct {
    Index     int              // 0-based position in the chain
    PrevHash  string           // hex SHA-256 of the previous block; "" for the genesis block
    Op        string           // "add" | "remove" | "rename"
    File      NetworkFileEntry // the file affected by this operation
    NewName   string           // only populated for "rename" operations
    Timestamp string           // RFC3339 UTC
    Signature []byte           // 64-byte ECDSA r||s; excluded when computing hash
}
```

`NetworkFileEntry` carries the file's metadata:

```go
type NetworkFileEntry struct {
    Name          string // filename
    Size          int    // bytes
    PrimaryNodeID int    // which node holds the primary shard set
    DateAdded     string // MM-DD-YYYY
    ContentHash   string // SHA-256 hex of original file bytes
}
```

### `UserChain`

A user's complete, append-only history.

```go
type UserChain struct {
    UserID    int          // numeric fingerprint of the user's public key
    Username  string       // display name (8-char hex fingerprint)
    PublicKey []byte       // PKIX DER P-256; used to verify all blocks in this chain
    Blocks    []ChainBlock // index 0 = genesis, append-only

    Files []NetworkFileEntry // in-memory only: ChainToFiles() result; never serialized
}
```

### `NetworkManifest`

The root structure.

```go
type NetworkManifest struct {
    Version   int         // 2 for the blockchain format
    UpdatedAt string      // RFC3339 UTC; updated on every write
    Chains    []UserChain // sorted ascending by UserID
}
```

---

## Block Hashing

The hash of a block is `SHA-256(json(block with Signature=nil))`. Zeroing the `Signature` field before hashing is what allows the signature to cover the full block content without a circular dependency.

```
blockHash(b):
    b.Signature = nil
    data = json.Marshal(b)
    return hex(SHA-256(data))
```

This hash serves two roles:
- It is the pre-image signed by ECDSA (`signBlock`)
- It is stored in the next block's `PrevHash` field (the chain link)

Because the hash is deterministic and covers every field of the block (index, prevHash, op, file, newName, timestamp), any change to any field produces a completely different hash.

---

## Block Signing

When a new block is created (in `AppendBlock`), it is signed immediately before being appended:

```
AppendBlock(chain, op, file, newName, kp):
    prevHash = blockHash(chain.Blocks[-1])   // or "" if empty
    b = ChainBlock{
        Index:     len(chain.Blocks),
        PrevHash:  prevHash,
        Op:        op,
        File:      file,
        NewName:   newName,
        Timestamp: now UTC,
    }
    hash = SHA-256(json(b with Signature=nil))
    r, s = ECDSA_Sign(kp.Private, hash)
    b.Signature = r (32 bytes) || s (32 bytes)
    chain.Blocks = append(chain.Blocks, b)
```

The signature is 64 bytes: the big-endian encoding of the ECDSA `(r, s)` pair, each zero-padded to 32 bytes. This matches the P-256 curve's 32-byte coordinate size.

---

## Chain Validation

`ValidateChain(chain)` is called on every chain received from a peer before it is accepted into the local manifest. It checks three things for each block:

1. **Sequential index** — `block.Index == i`
2. **Correct hash link** — `block.PrevHash == blockHash(chain.Blocks[i-1])` (or `""` for i=0)
3. **Valid signature** — `ECDSA_Verify(chain.PublicKey, blockHash(block), block.Signature)`

If any of these fail, the entire chain is rejected. This is conservative by design: a partially valid chain is not trusted because we cannot know which blocks are legitimate.

```
ValidateChain(chain):
    pub = ParsePublicKeyBytes(chain.PublicKey)
    prevHash = ""
    for i, b in chain.Blocks:
        if b.Index != i:        return false  // non-sequential
        if b.PrevHash != prevHash: return false  // broken link
        if !verifyBlock(b, pub): return false  // bad signature
        prevHash = blockHash(b)
    return true
```

---

## Replaying the Chain (ChainToFiles)

The current set of files is not stored directly — it is derived by replaying all blocks in order. `ChainToFiles` walks every block and applies each operation to an in-memory map:

```
ChainToFiles(chain):
    files = {}
    for b in chain.Blocks:
        switch b.Op:
        case "add":
            files[b.File.Name] = b.File
        case "remove":
            delete files[b.File.Name]
        case "rename":
            f = files[b.File.Name]
            delete files[b.File.Name]
            f.Name = b.NewName
            files[b.NewName] = f
    return values(files)
```

This produces the same result no matter how many times it is called on the same chain. It is pure and deterministic. Every peer replaying the same chain arrives at the exact same file set.

The result is cached in `UserChain.Files` (an in-memory field tagged `json:"-"`) so it does not need to be recomputed on every access.

---

## Merging Two Manifests

`MergeNetworkManifest(local, remote)` is called whenever a manifest arrives from a peer. It merges chain by chain:

```
MergeNetworkManifest(local, remote):
    merged = local
    changed = false
    for remoteChain in remote.Chains:
        if !ValidateChain(remoteChain):
            log "dropping invalid chain"
            continue
        i = FindChainIndex(merged, remoteChain.UserID)
        if i == -1:
            merged.Chains = insert(merged.Chains, remoteChain)  // new user
            changed = true
        else:
            winner = pickBetterChain(merged.Chains[i], remoteChain)
            if winner != merged.Chains[i]:
                merged.Chains[i] = winner
                changed = true
    return merged, changed
```

### Picking the Better Chain

```
pickBetterChain(a, b):
    if len(a.Blocks) > len(b.Blocks): return a
    if len(b.Blocks) > len(a.Blocks): return b
    // Same length — find first differing block
    for i in 0..len(a.Blocks):
        ha = blockHash(a.Blocks[i])
        hb = blockHash(b.Blocks[i])
        if ha != hb:
            return a if ha < hb else b  // lexicographic comparison
    return a  // identical chains
```

**Why longer chain wins:** A longer chain has more recorded operations, meaning the peer that holds it has observed more history. In the common case, this is simply the more up-to-date version.

**Why the fork rule works:** If two nodes both go offline at the same time, each appends a block, and then they reconnect — they have chains of the same length with different final blocks. The lexicographic comparison of the competing block hashes is deterministic: every peer in the network runs this same comparison and arrives at the same winner. No coordination is needed.

The losing chain's operations are discarded. This is the expected trade-off: in the presence of a true concurrent fork, one operation wins and the other is lost. For a personal file storage system this is acceptable — operations like "I uploaded photo.jpg on machine A while also uploading notes.md on machine B simultaneously" are rare, and if they do create a fork, one of those uploads survives in the canonical chain.

---

## What Happens on Connect

When two peers connect via the STUN server, each immediately pushes their local manifest to the other:

```
OnPeerAssigned(peer):
    ConnectToPeer(peer)
    go pushManifestToPeer(mosaicDir, client)
```

`pushManifestToPeer` reads the local manifest from disk, serializes it to JSON (`ManifestToJSON` removes the outer AES envelope), and sends it wrapped in a `ManifestSync` message.

On the receiving end:

```
OnMessageReceived(data):
    msg = DeserializeMessage(data)
    if msg.Type == ManifestSync:
        go handleManifestSync(mosaicDir, msg)
```

`handleManifestSync` calls `MergeNetworkManifest`, writes the result, and if anything changed, re-broadcasts the merged manifest to all currently connected peers. This is the convergence mechanism: new information fans out to the whole network even if a node was only directly connected to one peer when it joined.

---

## File Name Visibility

Chain blocks store file metadata in plaintext. Any peer who receives the manifest can see your file names, sizes, dates, and content hashes.

This is a deliberate design choice for the public permissionless network model. The alternative — per-block ECIES encryption of the file metadata — would preserve privacy but prevent peers from verifying chain integrity (they cannot hash what they cannot read).

In practice:
- **File content is never in the manifest.** The actual bytes are distributed as encrypted shards; the manifest only carries metadata.
- **Content hashes are one-way.** A SHA-256 hash of a file reveals nothing about its contents.
- **File names can be sensitive.** If this matters for your use case, the `File` field in `ChainBlock` can be ECIES-encrypted in a future version without changing the chain structure — the hash and signature would then cover the encrypted payload instead of the plaintext metadata.

---

## Key Derivation and Identity

Every user's signing key is derived deterministically from their login key:

```
seed = HKDF-SHA256(ikm=loginKey, salt=nil, info="mosaic-user-key", length=32)
ecdhKey = ecdh.P256().NewPrivateKey(seed)
privateKey = ecdhKeyToECDSA(ecdhKey)   // extract D, X, Y from raw bytes
```

The HKDF step is critical: it turns an arbitrary-length login key string into exactly 32 bytes of uniform entropy suitable for use as a P-256 private scalar. The `ecdh.P256().NewPrivateKey` call validates that the scalar is in `[1, N-1]` (the valid range for P-256).

`ecdhKeyToECDSA` converts the `*ecdh.PrivateKey` to `*ecdsa.PrivateKey` by extracting the raw scalar bytes and reconstructing the key structure with the curve coordinates. This is necessary because Go 1.22+ ignores the caller-supplied `io.Reader` in `ecdsa.GenerateKey`, making a deterministic ECDSA key impossible to derive through the normal path.

The result: the same login key on any machine produces the same keypair. A user can always reconstruct their signing key from their login key alone. Losing the device does not mean losing the ability to sign new blocks.

---

## Persistence of Signing Keys

The derived private key is cached at `~/.mosaic-user.key` in PEM format with `0600` permissions. The daemon reads this file on startup. If the file is missing (e.g., after a reinstall), the daemon detects that the session file references a missing key, clears the session, and prompts the user to log in again.

Re-running `mos login key <key>` re-derives the exact same key and overwrites the cache file. From the chain's perspective, nothing has changed — the public key embedded in `UserChain.PublicKey` still matches, and new blocks signed with the re-derived key pass verification.

---

## The Outer AES Encryption Layer

The entire `NetworkManifest` JSON is encrypted at rest using AES-256-GCM with a per-node key (`~/.mosaic-network.key`). This key is generated randomly on first run and never leaves the node.

This layer protects the manifest file on disk from being read by someone with physical access to the machine (or another user account on the same machine). It does not add any network-level protection — when the manifest is transmitted over P2P, the outer encryption is stripped and the inner JSON travels in plaintext (protected by the P2P transport itself, which uses DTLS via WebRTC).

Because this key is per-node, two nodes cannot decrypt each other's on-disk files. This is by design: the manifest's integrity guarantee comes from the blockchain (signatures + hash chain), not from the encryption. The AES layer is simply disk encryption.

---

## Security Model: Benefits and Liabilities

### What the chain protects against

**Impersonation.** Every block in your chain is signed with your ECDSA private key. A peer cannot forge a block on your behalf — they don't have your private key, and any block they construct will fail `verifyBlock` when checked against the public key embedded in your `UserChain`. This is verified on every peer before any chain is accepted via `ValidateChain`.

**History tampering.** The hash chain makes every block dependent on every block before it. If a peer modifies any historical block — changing a filename, a size, a timestamp — every subsequent `PrevHash` link becomes invalid. `ValidateChain` catches this at the first broken link. You cannot rewrite the past without invalidating the present.

**Retroactive chain shortening.** Once your chain has propagated to peers, you cannot present a shorter version to override it. The longer-chain-wins rule means any peer who has seen your full history will broadcast it back, and your shorter version will lose the merge comparison. History, once distributed, sticks.

**Invalid chains from untrusted peers.** `MergeNetworkManifest` runs `ValidateChain` on every incoming chain before touching the local state. A chain with a broken signature, a broken hash link, or a wrong index is dropped silently. A malicious peer cannot inject garbage into your manifest by sending a malformed chain.

**Identity recovery after device loss.** Your signing key is derived deterministically from your login key via HKDF-SHA256. Losing a device does not compromise your chain identity — re-running `mos login <key>` on any machine produces the exact same keypair, and new blocks you sign continue to validate against the `PublicKey` already embedded in your chain.

---

### What the chain does not protect against

**Lying about having files.** The manifest records file names and content hashes, but there is no cryptographic proof that the actual bytes exist anywhere on the network. A user can append `add "secret_data.txt"` blocks for files they never uploaded. Peers will believe the file exists, stubs will be created on their machines, and download attempts will silently fail. There is currently no storage proof — the chain is a claim ledger, not a verified storage ledger.

**Manifest spam.** There is no rate limiting or block cap per user. Nothing prevents someone from appending thousands of `add`/`remove` blocks in a loop. Every peer stores and gossips the entire manifest, so a determined attacker can bloat the manifest for every node on the network. The chain structure itself gives no defence here.

**Sybil attacks.** Your identity is any string passed to `mos login <key>`. There is no proof-of-work, no stake, and no authority that limits how many identities a single person can create. One person can generate thousands of keypairs, each with their own chain, and flood the manifest with arbitrary user entries and fake file histories.

**Strategic fork manipulation.** Fork resolution is deterministic, but the outcome depends on which chain is longer when two peers first exchange manifests. A node that stays online continuously and keeps appending blocks will always win a fork against a node that was offline. A malicious peer that controls the introduction point between two partitioned regions can also delay manifest propagation to influence which version reaches which peer first.

**No file namespace ownership.** There is no rule that prevents two users from both claiming the same filename in their respective chains. Both are valid — they belong to different chains. The result is two separate files with the same name owned by different users. The current UX does not handle this collision explicitly.

**Compromised private key — no revocation.** If your private key leaks, an attacker can append to your chain forever. The network has no mechanism to signal that a key has been revoked. Any block signed by the leaked key is indistinguishable from a legitimate block. The only recovery path is to abandon the identity and create a new one, losing the history associated with the old chain.

**File name privacy.** Chain blocks store file metadata in plaintext. Every peer who receives the manifest can read your file names, sizes, dates, and content hashes. File bytes never appear in the manifest, but the names alone can be sensitive. This is a deliberate trade-off for the current design: encrypting the metadata would prevent peers from verifying the chain (you cannot hash what you cannot read). Per-block encryption is architecturally possible in a future version without changing the chain structure.

---



| Approach | Tamper detection | History | Fork resolution | Complexity |
|---|---|---|---|---|
| Signed snapshot (v1) | Yes, per entry | No | By timestamp (lossy) | Low |
| Personal hash chain (v2, current) | Yes, per block | Yes | Deterministic by chain length + hash | Medium |
| Global blockchain (like Bitcoin) | Yes | Yes | Proof of work / stake | Very high |
| Merkle DAG (like Git) | Yes | Yes | Manual merge | High |

The personal hash chain is the right fit for Mosaic because:
- Each user owns their own chain — no global consensus needed
- Fork resolution is fully deterministic — no coordination or human intervention required
- The chain is small (one block per file operation) — transmission and verification are fast
- Any node can verify any chain without trusting the sender

---

## Code Locations

| Concept | File |
|---|---|
| Data structures, hashing, signing, validation, merge | `internal/fileSystem/networkManifest.go` |
| Key derivation | `internal/fileSystem/userKey.go` |
| Upload → AppendBlockAdd | `internal/daemon/handlers/uploadFile.go` |
| Delete → AppendBlockRemove | `internal/daemon/handlers/deleteFile.go` |
| Rename → AppendBlockRename | `internal/daemon/handlers/renameFile.go` |
| Peer connect → push + merge | `internal/daemon/handlers/joinNetwork.go` |
| List files from chain | `internal/daemon/handlers/listManifest.go` |
| Sync stubs from chain on startup/login | `internal/daemon/handlers/syncStubs.go` |
