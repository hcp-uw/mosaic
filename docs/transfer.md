# Mosaic Transfer Package

This package handles all file transfer between nodes: Reed-Solomon encoding, binary wire protocol, AES-256-GCM encryption, shard assembly, and file reconstruction.

---

## How a File Gets from Node A to Node B

### 1. Upload (`UploadFile`)

```
original file
    │
    ▼
SHA-256 hash (identifies the file permanently)
    │
    ▼
Reed-Solomon encode → 10 data shards + 4 parity shards = 14 total
    │
    ├── encrypt each shard in 32 KB chunks (AES-256-GCM)
    │   write to ~/Mosaic/.shards/<fileHash>/ in length-prefixed format
    │   fire shardStoredCb for each shard → records uploader in ShardMap
    │
    └── for each shard, sequentially:
            read each encrypted chunk from the shard file
            pack into binary frame (see Wire Protocol below)
            send to all peers via UDP
```

The sender uses a **token-bucket rate limiter** (20,000 tokens/sec, refilled every 10ms) to avoid overwhelming the receiver's UDP buffer. Shards are sent one at a time (semaphore=1) because parallel sends caused ~30% packet loss.

### 2. Receive (`HandleBinaryShardChunk`)

```
binary frame arrives via UDP
    │
    ▼
decode binary header (no JSON parsing)
    │
    ▼
store encrypted chunk as-is in shardAssembly
(no decryption — blind-courier model; see Encryption below)
    │
    ▼ (when all chunks for this shard arrive)
write shard to ~/Mosaic/.shards/<fileHash>/shard<N>_<fileHash>.dat
  in length-prefixed encrypted format
write meta.json alongside shards
fire shardStoredCb → records this node in ShardMap → manifest broadcast
    │
    ▼ (when all 10 data shards are on disk)
autoReconstruct fires — derives key, attempts decrypt
  if decrypt succeeds (we are the file owner): Reed-Solomon decode → ~/Mosaic/<filename>
  if decrypt fails (we are a peer storing for someone else): silently skip
```

### 3. Download (`FetchFileBytes`)

```
look up meta.json for filename in ~/Mosaic/.shards/*/
    │
    ├── meta not found locally?
    │     scan network manifest for file by name → get contentHash + fileSize
    │     write synthetic meta.json so the fetch can proceed
    │     if file not in manifest either → return error
    │
    ▼
check which data shards (0–9) are present locally
    │
    ├── all present?
    │     derive AES key from login key
    │     decrypt each shard to a temp plaintext dir
    │     Reed-Solomon decode → return bytes
    │
    └── some missing?
          for each missing shard: look up ShardMap in manifest → find holders
          send ShardRequest to each holder
          wait up to 60s for autoReconstruct to signal completion
          read reconstructed file from ~/Mosaic/<filename>
```

---

## Wire Protocol

Every shard chunk is sent as a raw binary UDP frame. This replaces the old JSON + base64 encoding, cutting bandwidth by ~28% and eliminating two marshal/unmarshal round trips per chunk.

The first byte is always `0x01` (the magic byte). JSON messages always start with `{` (`0x7B`), so the router can distinguish binary shard frames from JSON control messages with a single byte check.

**Frame layout** (all integers little-endian):

```
Offset  Size   Field
──────  ────   ─────────────────────────────────────────
0       1      magic byte (0x01)
1       32     fileHash — raw bytes (SHA-256, hex-decoded)
33      1      filename length (uint8)
34      N      filename (UTF-8)
34+N    4      fileSize — original file size in bytes (uint32)
38+N    1      shardIndex (uint8, 0-based)
39+N    4      chunkIndex (uint32, 0-based)
43+N    4      totalChunks (uint32)
47+N    1      totalDataShards (uint8)
48+N    1      totalShards (uint8)
49+N    4      data length (uint32)
53+N    M      AES-GCM encrypted chunk data
```

Total header overhead: ~55 bytes + filename length.
Each chunk: 32 KB plaintext → 32,796 bytes encrypted → ~32,851 bytes on wire.
Well under the 65,507-byte UDP maximum.

If a shard is smaller than 32 KB (e.g. small files produce small shards), `totalChunks = 1` and the single chunk contains only the real bytes — no zero-padding sent on the wire.

---

## Encryption

### Key Derivation

At login time the daemon derives the shard encryption key from the login key and caches it on disk:

```
mos login <key>
    │
    ▼
HKDF-SHA256(loginKey, info="mosaic-shard-key")  →  32-byte shard key
    │
    ▼
written to  ~/.mosaic-shard.key  (0600, raw 32 bytes)
```

At runtime, `shardEncryptionKey()` reads `~/.mosaic-shard.key` directly — no further key derivation is needed.

At logout, `~/.mosaic-shard.key` is deleted alongside `~/.mosaic-user.key`.

The raw login key is **never written to disk**. An attacker who obtains `~/.mosaic-shard.key` can decrypt shard data but cannot sign manifest blocks or impersonate the account (that requires `~/.mosaic-user.key`). The two files have different purposes and are kept separate.

Because the login key is the same on every device the user logs into, every device derives the same shard key. Different users have different login keys and therefore different shard keys — they cannot decrypt each other's shards.

### Blind-Courier Model (Option A)

Peers store encrypted shard blobs without ever decrypting them. Only the file owner (with the matching login key) can decrypt at reconstruction time.

**On the wire:** chunks are AES-256-GCM encrypted by the sender before framing.

**On a peer's disk:** encrypted chunks are stored as-is. `HandleBinaryShardChunk` does not call `decryptChunk` — it stores `c.data` (the ciphertext) directly in the shard assembly.

**On the owner's disk (at upload time):** shards are also stored in the encrypted format, so all shard files have the same format regardless of whether the node is the uploader or a peer.

**At reconstruction:** `decryptShardsToDir` reads the encrypted shard files, decrypts each chunk with the login-derived key, and writes plaintext to a temp directory. The RS decoder then operates on the plaintext. If decryption fails (wrong key), the node is not the file owner and reconstruction is skipped silently.

### Chunk Format

Each encrypted chunk is `[12-byte nonce] || [ciphertext + 16-byte GCM tag]`. A fresh nonce is generated per chunk, so even identical plaintext chunks produce different ciphertexts.

### On-Disk Shard Format

Shard files are NOT raw binary. They use a length-prefixed chunk format:

```
[4 bytes: totalChunks (little-endian)]
[4 bytes: chunk0 length]
[chunk0 encrypted data — nonce || ciphertext]
[4 bytes: chunk1 length]
[chunk1 encrypted data]
...
```

This lets `decryptShardToPlaintext` iterate chunks without seeking, and lets `StreamShardToPeer` forward individual chunks directly into binary frames without any decryption.

---

## Reed-Solomon Parameters

| Parameter       | Value |
|----------------|-------|
| Data shards    | 10    |
| Parity shards  | 4     |
| Total shards   | 14    |

Any 10 of the 14 shards are sufficient to reconstruct the original file. Up to 4 nodes can go offline and the file remains recoverable.

### Block Size

The block size is computed per file from the actual file size:

```
blockSize = ceil(fileSize / dataShards)   capped at 20 MB
```

This prevents small files (e.g. a 20 KB README) from producing disproportionately large shards. A 20 KB file with 10 data shards gets a 2 KB block size → 30 KB total shard output (1.5× the original). The block size is stored in `meta.json` so the decoder uses the exact same value.

---

## Local Shard Storage

Shards are stored at `~/Mosaic/.shards/<fileHash>/`:

```
~/Mosaic/.shards/
└── <fileHash>/
    ├── shard0_<fileHash>.dat    ← encrypted length-prefixed chunk format
    ├── shard1_<fileHash>.dat
    ├── ...
    ├── shard13_<fileHash>.dat
    └── meta.json
```

`meta.json` contains:

```json
{
  "fileName": "notes.md",
  "fileHash": "<sha256-hex>",
  "fileSize": 4096,
  "totalDataShards": 10,
  "totalShards": 14,
  "blockSize": 1024
}
```

`blockSize` is the shard block size used during RS encoding. The decoder must use the same value — stored here so reconstruction works correctly even after the encoder is gone.

---

## ShardMap and Shard Location Tracking

Every time a node stores a shard — whether from uploading or receiving — it records itself as a holder in the network manifest's `ShardMap`:

```
shardStoredCb fires (upload OR receive)
    │
    ▼
recordShardInManifest(contentHash, shardIndex)
    │
    ▼
RecordShardHolder(&manifest, contentHash, shardIndex, nodeID)
  → ShardMap[contentHash].Holders[shardIndex] = append(..., nodeID)
  → idempotent: duplicate nodeIDs are ignored
    │
    ▼
WriteNetworkManifestLocked → BroadcastNetworkManifest
```

`ShardMap` is a G-set CRDT: entries are only ever added, never removed. Merging two ShardMaps takes the union of holder lists per shard. This means the map converges to the same state on every node regardless of the order messages arrive.

When `FetchFileBytes` needs to request a missing shard, it calls `GetShardHolders(manifest, contentHash, shardIndex)` to find which peer IDs have it, then sends `ShardRequest` messages to each.

---

## Peer Join: Manifest Sync and Shard Redistribution

When a new peer connects (`OnPeerAssigned`):

### 1. Manifest push

`pushManifestToPeer` reads the local network manifest and sends it wrapped in a `ManifestSync` message. The new peer merges it with their own manifest via `MergeNetworkManifest`. If the merge brings in new data, the new peer broadcasts the combined result back.

### 2. Shard redistribution

`redistributeShardsToNewPeer` runs in a goroutine and routes shards to the new peer based on the rule:

```
targetPeerIndex = shardIndex % numPeers
```

Peers are ordered by sorting all node IDs (ours + all currently connected peers) lexicographically. This produces a stable ordering that every node can compute independently without coordination.

For each shard in each locally stored file, if `shardIndex % numPeers == newPeer's index`, `StreamShardToPeer` is called to forward it.

### `StreamShardToPeer`

Reads the encrypted shard file from disk, parses the length-prefixed chunks, and sends each as a binary frame to the target peer. No decryption or re-encryption — encrypted blobs are forwarded as-is through the rate limiter. The receiving peer's `HandleBinaryShardChunk` stores each chunk, `finalizeShard` reassembles the shard, and the new peer records itself in the ShardMap.

---

## Shard Request / Response

`ShardRequest` and `ShardResponse` are JSON control messages (low-frequency). They are used by `FetchFileBytes` when a specific shard is missing locally.

```
FetchFileBytes detects missing shard i
    │
    ▼
GetShardHolders(manifest, hash, i) → [nodeID1, nodeID2, ...]
    │
    ▼
SendToAllPeers(ShardRequest{hash, shardIndex: i})
    │
    ▼ (on holder node)
HandleShardRequest reads shard<i>_<hash>.dat → sends ShardResponse{data}
    │
    ▼ (back on requester)
handleShardResponse → StoreShardData → shard written to disk
  → shardStoredCb → ShardMap updated
  → if enough data shards: autoReconstruct → signals fileReadyChans
    │
    ▼
FetchFileBytes unblocks, reads reconstructed file
```

---

## Routing in the Daemon

In `internal/daemon/handlers/joinNetwork.go`, the `OnMessageReceived` callback checks the first byte:

```go
if len(data) > 0 && data[0] == 0x01 {
    go transfer.HandleBinaryShardChunk(data)
    return
}
// otherwise: JSON control message (ManifestSync, ShardRequest, ShardResponse, etc.)
```

In `internal/p2p/client.go`, `processPeerMessage` does the same check before attempting JSON deserialization, so binary frames never touch the JSON parser.

---

## Testing on One Node

You can verify the full encode → store → decode pipeline without any peers:

```bash
# 1. Install and start the daemon
./install.sh

# 2. Login (auth server must be running)
mos login account <username> <key>

# 3. Upload a file — shards are saved locally even with no peers
mos upload file /path/to/notes.md

# 4. Verify shards were written (all 14 in encrypted format)
ls ~/Mosaic/.shards/

# 5. Delete the original from ~/Mosaic so reconstruction is meaningful
rm ~/Mosaic/notes.md

# 6. Download — reconstructs from local shards
mos download file notes.md

# 7. Verify the file came back correctly
diff /path/to/notes.md ~/Mosaic/notes.md
```

No STUN server or second node needed for this test. The transfer package saves all 14 shards locally when it detects no peers are connected (`[Transfer] No peers connected — shards saved locally only`), and `FetchFileBytes` reads them back from disk, decrypting each shard before passing it to the RS decoder.
