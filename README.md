<div align="center">
  <img src="docs/assets/logo.png" alt="Mosaic" width="120" />

  # Mosaic
  ### The cloud that belongs to everyone.

  *A peer-to-peer cloud storage network where every participant contributes storage and gains access in return, meaning no central server, no single point of failure, no one in control. Don't trust us. Trust the code.*
</div>

---

## What Is Mosaic

Mosaic is a distributed file storage system built on direct peer-to-peer connections. When you upload a file, it is broken into shards and distributed across other nodes on the network. Anyone can join and contribute storage capacity. The more you share, the more you get back.

There is no central server that holds your files. The only server in the picture is a lightweight STUN coordinator that helps two nodes behind different routers find each other — once connected, all file transfer and metadata sync happen directly between peers.

**The network manifest is a blockchain.** Every user maintains a personal append-only chain of signed file operation blocks. Any peer can verify your chain's integrity using your public key alone — no trusted authority required. This makes Mosaic a fully public, permissionless network: anyone can join, contribute, and participate without asking permission.

---

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                        Your Machine                             │
│                                                                 │
│  ~/Mosaic/                                                      │
│  ├── notes.md              ← cached file (bytes on disk)        │
│  ├── photo.jpg.mosaic      ← stub (file is remote, not cached)  │
│  ├── .mosaic-manifest.json ← local index of your files          │
│  └── .mosaic-network-manifest ← blockchain manifest (all users) │
│                                                                 │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────────┐   │
│  │  Finder /    │    │  Menu Bar    │    │   mos (CLI)      │   │
│  │  Finder Sync │    │  App (Swift) │    │                  │   │
│  └──────┬───────┘    └──────┬───────┘    └────────┬─────────┘   │
│         │                  │ HTTP :7777            │ Unix socket │
│         │            ┌─────▼───────────────────────▼────────┐   │
│         └───badges──▶│          mosaic-node (daemon)        │   │
│                       │  • manifest read/write               │   │
│                       │  • blockchain block signing          │   │
│                       │  • file watcher (fsnotify)           │   │
│                       │  • P2P client (WebRTC/DTLS)          │   │
│                       └─────────────────┬────────────────────┘   │
└─────────────────────────────────────────┼───────────────────────┘
                                          │ UDP (WebRTC)
                              ┌───────────▼───────────┐
                              │     STUN/TURN Server  │
                              │  (hole punching only) │
                              └───────────┬───────────┘
                                          │
                              ┌───────────▼───────────┐
                              │      Peer Nodes        │
                              │  • hold your shards    │
                              │  • sync manifests      │
                              │  • relay transfers     │
                              └────────────────────────┘
```

### The Blockchain Manifest

Each user's file history is an append-only chain of ECDSA-signed blocks. Every upload, delete, and rename creates a new block linked to the previous one by SHA-256 hash. When you connect to a peer, you exchange manifests and each side keeps the longer valid chain. Forks resolve deterministically by comparing block hashes — no coordination needed, the whole network converges to the same state automatically.

Your identity is an ECDSA P-256 keypair derived deterministically from your login key using HKDF-SHA256. The same login key on any machine always produces the same keypair — log in on a new machine and you immediately recover your full file history.

---

## Getting Started

### Prerequisites

- **macOS** — the menu bar app and Finder integration are macOS-only (CLI works cross-platform)
- **Go 1.22+** — [golang.org/dl](https://golang.org/dl)
- **Xcode 15+** with an Apple ID signed in (for the menu bar app only)

### Install

```bash
git clone https://github.com/hcp-uw/mosaic.git
cd mosaic
chmod +x install.sh
./install.sh
```

This builds and installs two binaries:

| Binary | Location | Purpose |
|--------|----------|---------|
| `mos` | `/usr/local/bin/mos` | CLI — upload, download, manage files |
| `mosaic-node` | `/usr/local/bin/mosaic-node` | Daemon — handles all network operations in the background |

The daemon starts automatically. Verify it's running:

```bash
mos status network
```

### Log In

Before uploading anything you need to log in. Your login key is the seed for your identity on the network — the same key on any machine gives you access to your files.

```bash
mos login <your-key>
```

Check your login status:

```bash
mos login status
```

Your key is stored locally at `~/.mosaic-login.key`. Your ECDSA signing key is derived from it and cached at `~/.mosaic-user.key`. Neither leaves your machine.

### Join the Network

```bash
mos join network
```

This connects to the STUN server, performs UDP hole punching, and pairs you with a peer. Once connected, manifests sync automatically.

### Upload a File

```bash
mos upload file /path/to/notes.md
```

This:
1. Computes a SHA-256 content hash of the file
2. Appends an `"add"` block to your blockchain manifest, signed with your private key
3. Broadcasts the updated manifest to all connected peers
4. Creates a stub `notes.md.mosaic` in `~/Mosaic/` so Finder shows the file

### Open / Download a File

**Double-click the stub** in Finder, or:

```bash
mos download file notes.md
```

The daemon fetches the file bytes from the peer network, verifies the content hash, writes the real file to `~/Mosaic/notes.md`, and removes the stub.

### Delete a File

```bash
mos delete file notes.md        # delete from network + manifest
mos delete file -s notes.md     # remove local stub only (stays in manifest)
```

Or just delete the file or stub from Finder — the directory watcher detects it and handles the network update automatically.

---

## Menu Bar App

1. Open `MosaicApp/Mosaic.xcodeproj` in Xcode
2. Under **Signing & Capabilities**, set your Apple ID as development team for both the **Mosaic** and **MosaicFinderSync** targets
3. Press **⌘R**

The Mosaic icon appears in your menu bar. The app launches the daemon automatically if it isn't already running.

**Enable Finder badges:**
System Settings → Privacy & Security → Extensions → Added Extensions → check **MosaicFinderSync**

---

## CLI Reference

```
Account:
  login <key>                      Log in with your key
  login status                     Show current login status
  logout account                   Log out

Network:
  join network                     Join the storage network
  leave network                    Disconnect from the network
  status network                   Show connection state, role, peers, and storage
  peers network                    List connected peers

Node & Account:
  status node <node_id>            View information about a specific node
  status account                   View overall account status and all nodes

Storage:
  set storage <amount>             Set storage to share with the network
  empty storage                    Delete all stored data from the network

Files:
  list file                        List files with a local stub or cached copy on this machine
  list manifest                    List all files in your network manifest (cross-machine view)
  upload file <path>               Upload a file to the network
  upload folder <path>             Upload a folder to the network
  download file <name>             Download a file from the network
  download folder <name>           Download a folder from the network
  info file <name>                 Display information about a specific file
  info folder <name>               Display information about a specific folder
  delete file <name>               Delete a file from the network and manifest
  delete file -s <name>            Remove local stub only (file stays in manifest)
  delete folder <name>             Delete a folder from the network
  rename file <oldname> <newname>  Rename a file on the network

Other:
  version                          Display current Mosaic version
  shutdown                         Stop the daemon
  help                             Show all commands
```

---

## Architecture

### Components

| Component | Language | Role |
|-----------|----------|------|
| `mosaic-node` | Go | Background daemon: all network I/O, manifest management, watcher |
| `mos` | Go | CLI client that talks to the daemon via Unix socket |
| `MosaicApp` | Swift | Menu bar app and Finder extension |
| STUN server | Go | Deployed separately; handles UDP hole punching and leader election only |

### Key Files on Disk

```
~/.mosaic-login.key          Your login key (seed for all key derivation)
~/.mosaic-network.key        AES-256 key protecting the network manifest at rest
~/.mosaic-user.key           ECDSA P-256 private key (derived from login key)
~/.mosaic-session            Current session (userID, username, public key fingerprint)

~/Mosaic/
  .mosaic-manifest.json      Local file index (plaintext JSON)
  .mosaic-network-manifest   Blockchain manifest (AES-256-GCM encrypted on disk)
  <filename>                 Cached real file
  <filename>.mosaic          Stub placeholder for remote-only files
```

### Network Manifest — Blockchain

The network manifest is a collection of per-user chains. Each chain is an append-only log of signed operations:

```
User A's chain:
  block[0]  op:"add"    file:"notes.md"   prevHash:""          signed by A
  block[1]  op:"add"    file:"photo.jpg"  prevHash:hash(b[0])  signed by A
  block[2]  op:"remove" file:"notes.md"   prevHash:hash(b[1])  signed by A

User B's chain:
  block[0]  op:"add"    file:"report.pdf" prevHash:""          signed by B
```

Current file state = replay all blocks in order. Merge = longer valid chain wins per user. Forks resolve deterministically by comparing block hashes at the point of divergence.

---

## Documentation

| Doc | What it covers |
|-----|---------------|
| [docs/quickstart.md](docs/quickstart.md) | Developer quickstart — build, install, run |
| [docs/manifest.md](docs/manifest.md) | Full manifest system: local manifest, stubs, blockchain network manifest, P2P sync |
| [docs/manifest-blockchain.md](docs/manifest-blockchain.md) | Deep dive: block hashing, signing, fork resolution, key derivation |
| [docs/filesystem.md](docs/filesystem.md) | fileSystem package: ~/Mosaic/ directory, stubs, manifest API |
| [docs/daemon.md](docs/daemon.md) | Daemon internals: Unix socket, HTTP API, filesystem watcher |
| [docs/stun.md](docs/stun.md) | STUN server: UDP hole punching, leader election, liveness |
| [docs/p2p.md](docs/p2p.md) | P2P client package structure |
| [docs/transfer.md](docs/transfer.md) | File transfer: Reed-Solomon shards, binary wire protocol, AES-256-GCM |
| [docs/deploy.md](docs/deploy.md) | Deploying the STUN/TURN server |
| [docs/menu-bar-app.md](docs/menu-bar-app.md) | macOS menu bar app and Finder extension |
| [docs/tapestry.md](docs/tapestry.md) | Tapestry: distributed event log design (in progress) |

---

## Development

```bash
# Build and install, then start the daemon with debug output
./install.sh -d

# Rebuild and restart quickly
make restart

# Check daemon status
make status

# Stop everything
make stop
```

Daemon logs:
```bash
tail -f /tmp/mosaicd.log
```

---

## Troubleshooting

**`mos status network` fails**
The daemon isn't running. Run `./install.sh` or start it manually:
```bash
mosaic-node > /tmp/mosaicd.log 2>&1 &
```

**Upload says "not logged in"**
Run `mos login <your-key>` before uploading. Uploads require a signing key — without it the daemon cannot append to your manifest chain.

**Files don't show in `mos list file` after login on a new machine**
Run `mos join network` to sync the manifest from peers. Stubs for your remote files are created automatically after the first manifest sync.

**Finder badges don't show**
System Settings → Privacy & Security → Extensions → Added Extensions → check **MosaicFinderSync**. Restart Finder if needed:
```bash
killall Finder
```

**File shows wrong size**
Delete the stub and re-upload:
```bash
mos delete file -s notes.md
mos upload file /path/to/notes.md
```

**Daemon logs**
```bash
tail -f /tmp/mosaicd.log
```
