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
    ├── save all 14 shards locally to ~/Mosaic/.shards/<fileHash>/
    │   (so the sender can reconstruct its own file later)
    │
    └── for each shard, sequentially:
            split shard into 32 KB chunks
            AES-256-GCM encrypt each chunk
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
AES-256-GCM decrypt chunk data
    │
    ▼
insert chunk into shardAssembly (per-shard mutex, not global)
    │
    ▼ (when all chunks for this shard arrive)
write shard to ~/Mosaic/.shards/<fileHash>/shard<N>_<fileHash>.dat
write meta.json alongside shards
    │
    ▼ (when all 10 data shards are on disk)
Reed-Solomon decode → original file
write to ~/Mosaic/<filename>
```

### 3. Download (`FetchFileBytes`)

Called when a node already has the shards locally (e.g. after receiving them, or after uploading):

```
scan ~/Mosaic/.shards/*/meta.json for matching filename
    │
    ▼
verify all 10 data shards exist on disk
    │
    ▼
Reed-Solomon decode → original file bytes
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

---

## Encryption

Shard data is encrypted with **AES-256-GCM** before it goes on the wire.

The key is derived from the user's login key using **HKDF-SHA256** with the info string `"mosaic-shard-key"`. Because every node logs in with the same credentials, every node derives the same 32-byte encryption key — no key exchange needed.

```
loginKey  ──HKDF-SHA256──►  32-byte AES-256 shard key
```

Each chunk gets a fresh random 12-byte nonce prepended to its ciphertext:

```
[12-byte nonce] || [ciphertext + 16-byte GCM tag]
```

This means even two identical chunks produce different ciphertexts on the wire.

---

## Reed-Solomon Parameters

| Parameter       | Value |
|----------------|-------|
| Data shards    | 10    |
| Parity shards  | 4     |
| Total shards   | 14    |
| Block size     | 20 MB |

Any 10 of the 14 shards are sufficient to reconstruct the original file. Up to 4 nodes can go offline and the file remains recoverable.

The block size of 20 MB means even a 1 MB file produces 20 MB of shard data (the encoder zero-pads). This is a known inefficiency — a smaller block size would reduce chunk counts for small files.

---

## Local Shard Storage

Shards are stored at `~/Mosaic/.shards/<fileHash>/`:

```
~/Mosaic/.shards/
└── <fileHash>/
    ├── shard0_<fileHash>.dat
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
  "totalShards": 14
}
```

This sidecar file lets any node look up a file by name and reconstruct it without needing to decrypt the network manifest.

---

## Routing in the Daemon

In `internal/daemon/handlers/joinNetwork.go`, the `OnMessageReceived` callback checks the first byte:

```go
if len(data) > 0 && data[0] == 0x01 {
    go transfer.HandleBinaryShardChunk(data)
    return
}
// otherwise: JSON control message (ManifestSync, ShardRequest, etc.)
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

# 4. Verify shards were written
ls ~/Mosaic/.shards/

# 5. Delete the original from ~/Mosaic so reconstruction is meaningful
rm ~/Mosaic/notes.md

# 6. Download — reconstructs from local shards
mos download file notes.md

# 7. Verify the file came back correctly
diff /path/to/notes.md ~/Mosaic/notes.md
```

No STUN server or second node needed for this test. The transfer package saves all 14 shards locally when it detects no peers are connected (`[Transfer] No peers connected — shards saved locally only`), and `FetchFileBytes` reads them back from disk.
