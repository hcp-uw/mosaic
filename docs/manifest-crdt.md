# Mosaic Manifest CRDT

This document explains, in depth, how the network manifest works as a signed Last-Write-Wins (LWW) Set CRDT — why this design was chosen, how the cryptographic pieces fit together, and what guarantees it provides for a public permissionless network.

---

## Why a CRDT Instead of a Blockchain?

Mosaic is a public network. Anyone with the CLI can join, connect to the STUN server, and receive a copy of the network manifest. This means integrity cannot rely on trusting peers — a malicious node could receive your file list and send back a modified version to other peers.

### The v1 Problem (Single Signed Snapshot)
The original v1 design used ECIES encryption combined with ECDSA signatures over the entire file list as a single blob. This was good for tamper detection of individual snapshots but had structural weaknesses: there was no way to distinguish "this user deleted a file" from "a peer dropped that file from the blob," and history was invisible.

### The v2 Problem (Personal Hash Chain)
v2 introduced a personal hash chain (blockchain): each file operation became an append-only signed block, and `SHA-256(prevBlock)` was stored in each new block. This gave tamper-evident history but had a fatal scaling problem: the chain grew with every file operation forever. 100 files added and removed = 200 blocks. 1000 add/remove cycles = 2000 blocks, all transmitted in full on every manifest sync.

### The v3 Solution (LWW-Set CRDT)
The v3 design is a **signed Last-Write-Wins Set** (LWW-Set) CRDT. Instead of recording every operation, only the *current state* is kept: one signed `FileRecord` per file per user. The key insight is that Mosaic only ever needs to answer "does this user currently have this file?" — not "what operations led to this state?"

Tamper resistance comes from two mechanisms:
1. **ECDSA signatures** — each record is signed; forgery requires the owner's private key.
2. **Monotonic sequence numbers** — each mutation increments a `Seq` counter, preventing replay of older state. A peer that receives `Seq=5` cannot later trick another peer into accepting `Seq=3`.

The tradeoff: you lose "I can prove the exact sequence of operations in my history." You keep "the current state is authentic and cannot be forged or replayed."

---

## Data Structures

### `FileRecord`

One entry in a user's current file set.

```go
type FileRecord struct {
    ContentHash   string // SHA-256 hex of original file bytes (public)
    EncryptedMeta []byte // AES-256-GCM: {name, size, dateAdded} — owner-only
    Seq           uint64 // monotonically increasing per (userID, contentHash)
    Deleted       bool   // true = tombstone; this file is no longer in the set
    Timestamp     string // RFC3339 UTC; informational only, not signed
    Signature     []byte // 64-byte ECDSA r||s; covers all fields above
}
```

**Why `Seq` instead of a timestamp?** Timestamps are not reliable for distributed ordering — clocks drift, and a malicious node could backdate entries. `Seq` is a counter that only the owner controls (they sign each increment), making it tamper-evident and monotonic.

### `UserState`

One user's complete LWW-Set.

```go
type UserState struct {
    UserID    int                    // numeric fingerprint of the user's public key
    Username  string                 // display name (8-char hex fingerprint)
    PublicKey []byte                 // PKIX DER P-256; verifies all records in this set
    Records   map[string]*FileRecord // contentHash → FileRecord (one per file)
}
```

### `NetworkManifest`

The root structure.

```go
type NetworkManifest struct {
    Version   int                        // 3 for the LWW-Set CRDT format
    UpdatedAt string                     // RFC3339 UTC; updated on every write
    Users     map[int]*UserState         // userID → UserState
    ShardMap  map[string]*ShardLocations // contentHash → shard-holder G-set (keyed by stable STUN node ID)
}
```

---

## Record Signing

Every `FileRecord` is ECDSA-signed by the owner immediately after creation. The signed payload is a deterministic byte sequence:

```
canonical_payload =
    big_endian_uint32(userID)       ||
    contentHash (ASCII bytes)        ||
    big_endian_uint64(seq)           ||
    deleted_byte (0x00 or 0x01)      ||
    encryptedMeta (raw bytes)
```

```
hash = SHA-256(canonical_payload)
r, s = ECDSA_Sign(owner_private_key, hash)
record.Signature = r (32 bytes) || s (32 bytes)
```

The payload commits to every field that matters for correctness:
- `userID` — prevents a record from being accepted as belonging to a different user
- `contentHash` — commits to which file this record describes
- `Seq` — commits to the version; replay of an old `Seq` fails verification against the newer locally-held record
- `deleted` — the tombstone flag is signed, so a peer cannot flip "deleted" to claim a file is back
- `encryptedMeta` — the encrypted name/size/date is committed, so it cannot be swapped without invalidating the signature

`Timestamp` is intentionally excluded from the signed payload — it is informational only and cannot be trusted for ordering.

---

## Mutations

### Add a file

```
RecordFileAdd(m, userID, username, file, kp):
    us = m.Users[userID]  // create if absent
    nextSeq = (us.Records[file.ContentHash].Seq + 1) or 1 if absent

    metaKey = SHA-256(kp.Private.D || "mosaic-block-meta")
    encMeta = AES-256-GCM(metaKey, json({name, size, dateAdded}))

    r = FileRecord{
        ContentHash:   file.ContentHash,
        EncryptedMeta: encMeta,
        Seq:           nextSeq,
        Deleted:       false,
        Timestamp:     now UTC,
    }
    sign r using kp.Private
    us.Records[file.ContentHash] = r
```

### Remove a file

```
RecordFileRemove(m, userID, contentHash, kp):
    existing = m.Users[userID].Records[contentHash]
    nextSeq = existing.Seq + 1

    r = FileRecord{
        ContentHash: contentHash,
        Seq:         nextSeq,
        Deleted:     true,
        Timestamp:   now UTC,
    }
    sign r using kp.Private
    m.Users[userID].Records[contentHash] = r
```

The old record is replaced — not accumulated. The manifest stays O(N files), not O(N operations).

### Rename a file

```
RecordFileRename(m, userID, contentHash, newName, kp):
    existing = m.Users[userID].Records[contentHash]
    existingMeta = AES-256-GCM-Decrypt(metaKey, existing.EncryptedMeta)

    encMeta = AES-256-GCM(metaKey, json({name: newName, size: existingMeta.Size, dateAdded: existingMeta.DateAdded}))

    r = FileRecord{
        ContentHash:   contentHash,
        EncryptedMeta: encMeta,   // same bytes, new name
        Seq:           existing.Seq + 1,
        Deleted:       false,
        Timestamp:     now UTC,
    }
    sign r using kp.Private
    m.Users[userID].Records[contentHash] = r
```

The `ContentHash` is stable across a rename (file bytes don't change). Only the encrypted name changes, and `Seq` increments to version the update.

---

## Getting the Current File Set

```
GetUserFiles(m, userID, metaKey):
    us = m.Users[userID]
    result = []
    for contentHash, record in us.Records:
        if record.Deleted:
            continue
        entry = NetworkFileEntry{ContentHash: contentHash}
        if metaKey is not nil:
            entry.Name, entry.Size, entry.DateAdded = decrypt(record.EncryptedMeta, metaKey)
        result = append(result, entry)
    return result
```

Non-owners pass `nil` for `metaKey` and see only `ContentHash`. Owners pass their derived key and see full metadata.

---

## Merging Two Manifests (LWW)

`MergeNetworkManifest(local, remote)` is called whenever a manifest arrives from a peer. The rule is simple: **higher `Seq` wins**. For each `(userID, contentHash)` pair, whichever record has the higher `Seq` is kept. Records with invalid signatures are silently dropped.

```
MergeNetworkManifest(local, remote):
    merged = local
    changed = false

    for userID, remoteUser in remote.Users:
        pub = ParsePublicKeyBytes(remoteUser.PublicKey)
        localUser = merged.Users[userID]  // create if absent
        
        for contentHash, remoteRecord in remoteUser.Records:
            if !verifyRecord(userID, remoteRecord, pub):
                drop silently  // invalid signature
                continue
            
            localRecord = localUser.Records[contentHash]
            if localRecord is nil or remoteRecord.Seq > localRecord.Seq:
                localUser.Records[contentHash] = remoteRecord
                changed = true
            // Equal Seq: idempotent — keep local

    merge ShardMaps as G-set union
    return merged, changed
```

### Why LWW is correct here

- **No true concurrent conflicts.** A user's file set is only modified by one actor: the owner. Two peers can hold different snapshots of the owner's state, but only the owner can produce a record with a new `Seq`. When peers exchange manifests, the higher `Seq` reflects the more recent operation.
- **Idempotent.** Merging the same manifest twice produces no change.
- **Convergent.** No matter what order manifests arrive, every peer eventually converges to the same state (all see the same highest-`Seq` record for each `(userID, contentHash)` pair).
- **Tamper-evident.** An attacker cannot fabricate a higher-`Seq` record without the owner's private key. An attacker cannot replay a lower-`Seq` record to roll back state (it is rejected as non-strictly-greater).

---

## File Name Privacy

File names, sizes, and dates are **AES-256-GCM encrypted** at the record level using a key derived from the owner's private key:

```
metaKey = SHA-256(kp.Private.D || "mosaic-block-meta")
```

Only the owner can decrypt their own file names. Peers receive `FileRecord`s where `EncryptedMeta` is an opaque byte blob; only `ContentHash` travels in plaintext.

This is a cryptographic guarantee, not a display-layer filter — the ciphertext is part of the signed payload, so there is no plaintext version of the metadata anywhere in the distributed manifest. A peer storing your shards knows the content hash needed to serve them but cannot read your file names, sizes, or dates.

---

## What the CRDT Protects Against

| Threat | Mitigation |
|---|---|
| Forging a file record | Requires owner's ECDSA private key; any forged record fails `verifyRecord` on merge |
| Replaying an old record to roll back state | `Seq` must be strictly greater than the locally-held value; equal-or-lower is ignored |
| Tampering with `ContentHash`, `Seq`, or `Deleted` after signing | Invalidates the ECDSA signature; dropped on merge |
| Swapping the encrypted metadata blob | Invalidates the ECDSA signature (encMeta is in the signed payload) |
| Injecting garbage chains from untrusted peers | Each record is independently verified; bad records are dropped without affecting the rest |

---

## What the CRDT Does Not Protect Against

| Limitation | Explanation |
|---|---|
| No operation history | Only the current state is kept. You cannot audit "when did this file get renamed?" |
| Key compromise | If the private key leaks, an attacker can create records with arbitrarily high `Seq` and override any state. No revocation mechanism exists. |
| Sybil attacks | Any string can be used as a login key; one person can generate thousands of identities. The per-IP STUN registration cap reduces broadcast amplification but does not prevent multi-identity users. |
| File name privacy is only as strong as the login key | The encryption key derives from the private key which derives from the login key. Obtaining the login key allows decryption of all metadata. |
| Manifest entry is not itself a possession proof | `FileRecord` is a claim: a user can record a file they never actually stored. Storage proofs (ShardProbe challenges, run by the periodic audit) verify that peers hold the *shards* they claim in the ShardMap and evict liars, but they do not validate the `FileRecord` entry itself. |

---

## What Happened to the Old Manifests?

v2 manifests (blockchain format, `"version": 2`) are treated as empty on first read — no migration. Since Mosaic is not yet in production, old chain data was discarded rather than ported to the new format.

---

## Code Locations

| Concept | File |
|---|---|
| Data structures, signing, verification, merge | `internal/fileSystem/networkManifest.go` |
| Key derivation | `internal/fileSystem/userKey.go` |
| Upload → RecordFileAdd | `internal/daemon/handlers/uploadFile.go` |
| Delete → RecordFileRemove | `internal/daemon/handlers/deleteFile.go` |
| Rename → RecordFileRename | `internal/daemon/handlers/renameFile.go` |
| Peer connect → push + merge | `internal/daemon/handlers/joinNetwork.go` |
| List files | `internal/daemon/handlers/listManifest.go` |
| Sync stubs on startup/login | `internal/daemon/handlers/syncStubs.go` |
