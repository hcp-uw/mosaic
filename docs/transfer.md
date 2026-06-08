# Mosaic Transfer Package

This package handles all file transfer between nodes: Reed-Solomon encoding, binary wire protocol, AES-256-GCM encryption, shard assembly, and file reconstruction. The code is split across several files in `internal/transfer/`:

| File | Responsibility |
|------|---------------|
| `transfer.go` | Package-level state: constants, types, globals, callbacks, progress counters |
| `wire.go` | Binary frame encode/decode, adaptive UDP pacer, `sendFrameViaQUIC` |
| `meta.go` | Shard metadata I/O (`meta.json`), `FindShardMeta*`, `missingDataShards` |
| `crypto.go` | AES-256-GCM encryption/decryption, encrypted shard file I/O |
| `upload.go` | `UploadFile`, `sendPlaintextShardToPeer` |
| `receive.go` | `HandleBinaryShardChunk`, `finalizeShard`, `autoReconstruct`, `StoreShardData` |
| `download.go` | `FetchFileBytes`, `DeleteLocalShards` |
| `serve.go` | `StreamShardToPeer`, `HandleShardRequest`, `HandleShardStreamDone`, `HandleShardStreamAck` |

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
    ├── for each shard that maps to this node:
    │     encrypt in 8 KB chunks (AES-256-GCM)
    │     write to ~/Mosaic/.shards/<fileHash>/ in length-prefixed format
    │     fire shardStoredCb → records uploader in ShardMap
    │
    └── for each shard that maps to a peer:
          also store encrypted copy locally (for serving re-requests)
          fire shardStoredCb → records uploader in ShardMap
          open QUIC stream to peer (falls back to UDP if not established)
          encrypt each 8 KB chunk and send as binary frame
          after all chunks: send ShardStreamDone
```

The sender uses an **adaptive pacer (AIMD)**: it starts at 2 ms between sends (500 sends/sec) and adjusts based on kernel write latency — speeding up when the OS send buffer has headroom, slowing down when it backs up. Each shard is sent to its target peer in a rate-limited goroutine; sends to different peers run concurrently.

### 2. Receive (`HandleBinaryShardChunk`)

```
binary frame arrives via QUIC or UDP
    │
    ▼
decode binary header (no JSON parsing)
    │
    ▼
store encrypted chunk as-is in shardAssembly
(no decryption — blind-courier model; see Encryption below)
    │
    ├── signal shardActivityChans → FetchFileBytes idle timer resets immediately
    │
    ▼ (when all chunks for this shard arrive)
write shard to ~/Mosaic/.shards/<fileHash>/shard<N>_<fileHash>.dat
  in length-prefixed encrypted format
write meta.json alongside shards
signal shardReadyChans → unblocks FetchFileBytes for this shard
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
          if no P2P client (not joined to the network):
            return error: "N/M data shards missing — run 'mos join' to fetch from peers"
          for each missing shard (one at a time):
            register shardReadyChans + shardActivityChans for this shard
            send ShardRequest to all peers
            wait: shardCh fires (shard landed) OR activityCh fires (chunk arrived, reset idle timer)
            OR idle timer expires (10s first-chunk, 30s between chunks)
          after all shards: wait for autoReconstruct to signal fileReadyChans
          read reconstructed file from ~/Mosaic/<filename>
```

Shards are requested one at a time to avoid launching N concurrent `StreamShardToPeer` goroutines that would fight each other for bandwidth.

---

## Wire Protocol

Every shard chunk is sent as a raw binary frame over QUIC (preferred) or UDP. This replaced the old JSON + base64 encoding, cutting bandwidth by ~28% and eliminating marshal/unmarshal overhead per chunk.

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
Each chunk: 8 KB plaintext → ~8,204 bytes encrypted (12-byte nonce + 16-byte GCM tag) → ~8,259 bytes on wire.  
Well under the 65,507-byte UDP maximum; fragments into ~6 IPv4 packets at 1500-byte MTU.

If a shard is smaller than 8 KB (e.g. small files produce small shards), `totalChunks = 1` and the single chunk contains only the real bytes — no zero-padding sent on the wire.

### QUIC transport

`StreamShardToPeer` and `sendPlaintextShardToPeer` both try to open a QUIC unidirectional send stream first (`client.OpenShardStream`). QUIC provides reliable, in-order delivery with no per-packet retransmit overhead at the application layer. If the QUIC connection isn't established yet, the function falls back to UDP with the adaptive pacer.

Only the lexicographically smaller P2P ID dials the QUIC connection — the other side accepts. This prevents both nodes from dialing simultaneously and creating duplicate connections.

---

## Selective Retransmit: ShardStreamDone / ShardStreamAck

After sending all chunks of a shard, the sender transmits a JSON `ShardStreamDone` message:

```json
{ "type": "shard_stream_done", "fileHash": "...", "shardIndex": 2, "totalChunks": 1280 }
```

The receiver (`HandleShardStreamDone`) **always** replies with a `ShardStreamAck`:

```json
{ "type": "shard_stream_ack", "fileHash": "...", "shardIndex": 2, "missingChunks": [] }
```

`missingChunks` is empty if the receiver has all chunks. If it is missing some, the list contains their indices:

```json
{ "type": "shard_stream_ack", "fileHash": "...", "shardIndex": 2, "missingChunks": [47, 312, 891] }
```

Three cases handled by `HandleShardStreamDone`:
1. Shard file already on disk (assembly completed) → `ack(missing=[])`
2. No assembly entry for this shard (all chunks were dropped) → `ack(missing=[0..totalChunks-1])`
3. Active assembly present → compute missing indices and `ack(missing=[...])`

**Synchronous ack loop (sender side):** The sender blocks after `ShardStreamDone`, waiting up to 30 seconds for the `ShardStreamAck` on a per-shard channel keyed `"fileHash:shardIndex:peerID"`. If the ack reports missing chunks, only those chunks are retransmitted via O(missing) disk seeks — no full shard re-read. The loop continues sending `ShardStreamDone` and waiting for `ShardStreamAck` until the receiver confirms `missing=[]`, or the 30-second timeout fires. No full shard re-request is ever needed.

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

**Blind-courier privacy:** For nodes storing shards on behalf of another user, `fileName` and `fileSize` are **omitted** from `meta.json` (the fields carry `omitempty`). A peer shard directory holds only the RS parameters needed to serve and relay the shard — it reveals nothing about the file it belongs to. `fileName` and `fileSize` are written only by the file owner, either at upload time or when `EnsureShardMeta` runs during `FetchFileBytes` and discovers an existing privacy-stripped meta.

`EnsureShardMeta` is idempotent for complete meta (where `fileName` is already set), but will update stripped meta (no `fileName`) when the file owner calls it — preserving existing RS parameters and filling in the missing identity fields.

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

When `FetchFileBytes` needs to request a missing shard, it calls `GetShardHolders(manifest, contentHash, shardIndex)` to find which peer IDs have it, then sends a `ShardRequest` to all peers.

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

Reads the encrypted shard file from disk, parses the length-prefixed chunks, and sends each as a binary frame to the target peer via QUIC (or UDP fallback). No decryption or re-encryption — encrypted blobs are forwarded as-is. The receiving peer's `HandleBinaryShardChunk` stores each chunk, `finalizeShard` reassembles the shard, and the new peer records itself in the ShardMap. After all chunks are sent, `ShardStreamDone` is sent so the receiver can trigger selective retransmit for any dropped chunks.

---

## Shard Request / Response

`ShardRequest` is a JSON control message sent when `FetchFileBytes` detects a missing shard. The holder responds by calling `StreamShardToPeer` — which sends the shard as binary frames, not a single JSON response. `ShardResponse` (old JSON-body response) is no longer used for the data path.

```
FetchFileBytes detects missing shard i
    │
    ▼
SendToAllPeers(ShardRequest{hash, shardIndex: i})
    │
    ▼ (on holder node)
HandleShardRequest → go StreamShardToPeer(...)
  → binary frames sent chunk by chunk via QUIC/UDP
  → ShardStreamDone sent at end
    │
    ▼ (back on requester)
HandleBinaryShardChunk stores each chunk → finalizeShard assembles shard
  → shardReadyChans signaled → FetchFileBytes advances to next shard
  → shardStoredCb → ShardMap updated
  → if enough data shards: autoReconstruct → signals fileReadyChans
    │
    ▼
FetchFileBytes unblocks, reads reconstructed file
```

If chunks are dropped (UDP), `ShardStreamDone` triggers `ShardStreamAck` with missing indices, and the sender retransmits only those chunks inline — no full re-request needed. See [Selective Retransmit](#selective-retransmit-shardstreamdone--shardstreamack) above.

---

## Storage Proofs (ShardProbe)

Before redistributing a shard to a new peer, Mosaic verifies that the candidate peer actually holds the shard bytes — not just claims to in the ShardMap. This prevents a peer from lying about its storage and receiving shards it would duplicate without holding its share.

### Challenge-Response Protocol

```
Prober (us)                         Holder (peer)
    │                                    │
    │── ShardProbe{fileHash, shardIdx, nonce} ──►│
    │                                    │
    │    computes SHA-256(nonce || shard_bytes)   │
    │◄── ShardProbeAck{..., contentHash} ─────────│
    │                                    │
compare contentHash against our own
SHA-256(nonce || our copy of the shard)
```

1. We read our local copy of the shard and compute `SHA-256(nonce || shard_bytes)` — this is the expected hash.
2. We send a `ShardProbe` with a random 16-byte nonce. The nonce prevents pre-computation; a peer cannot know the expected hash without reading the actual shard bytes on demand.
3. The peer reads its shard, computes `SHA-256(nonce || shard_bytes)`, and sends a `ShardProbeAck` with the result.
4. We compare. If the hashes match, the proof passes. If they differ — or if no response arrives within 3 seconds — the proof fails.

### Failure Handling

`RecordProbeResult(peerID, success)` in `p2p/peer.go` tracks consecutive probe failures per peer. After 3 consecutive failures, the peer is evicted: removed from `p2p.Client.peers` and notified via `OnPeerLeft`, which removes the peer from the ShardMap and triggers shard redistribution.

A single probe failure does not evict — it might be a transient shard load error. Three consecutive failures indicate the peer is systematically lying about its storage.

Proof results only count when we have a local copy of the shard (so we can compute the expected hash). If we don't hold the shard, we skip the probe rather than recording a false failure.

### Message Types

```
api.ShardProbe    → internal/transfer/probe.go:HandleShardProbe
api.ShardProbeAck → internal/transfer/probe.go:HandleShardProbeAck
```

These are dispatched via callbacks registered in `daemon/handlers/joinNetwork.go` to avoid an import cycle (`transfer` imports `p2p`, so the dispatch is done in the daemon and forwarded to the transfer package).

### Code Locations

| Function | File |
|---|---|
| `ProbeShardAtPeer` — initiates the challenge, waits for response | `internal/transfer/probe.go` |
| `HandleShardProbe` — responds to incoming challenges | `internal/transfer/probe.go` |
| `HandleShardProbeAck` — delivers response to the waiting caller | `internal/transfer/probe.go` |
| `RecordProbeResult` / `maxProbeFailures=3` | `internal/p2p/peer.go` |
| Callback registration to break import cycle | `internal/daemon/handlers/joinNetwork.go` |

---

## Routing in the Daemon

In `internal/daemon/handlers/joinNetwork.go`, the `OnMessageReceived` callback checks the first byte:

```go
if len(data) > 0 && data[0] == 0x01 {
    go transfer.HandleBinaryShardChunk(data)
    return
}
// otherwise: JSON control message (ManifestSync, ShardRequest, ShardStreamDone, etc.)
```

In `internal/p2p/client.go`, `processPeerMessage` does the same check before attempting JSON deserialization, so binary frames never touch the JSON parser.

---

## Testing on One Node

You can verify the full encode → store → decode pipeline without any peers:

```bash
# 1. Install and start the daemon
./install.sh

# 2. Login
mos login <key>

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

No STUN server or second node needed for this test. The transfer package saves all 14 shards locally when it detects no peers are connected (`[Transfer] No peers connected — all shards saved locally`), and `FetchFileBytes` reads them back from disk, decrypting each shard before passing it to the RS decoder.
