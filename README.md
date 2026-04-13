# Mosaic
### The cloud that belongs to everyone.

Mosaic is a peer-to-peer cloud storage network. Instead of uploading to a central server, files are distributed as shards across trusted nodes using Reed-Solomon erasure coding. Every participant contributes storage and gains access to the network in return.

---

## Getting started

### Prerequisites

- **macOS** (the menu bar app and Finder integration are macOS-only)
- **Go 1.21+** — [golang.org/dl](https://golang.org/dl)
- **Xcode 15+** with an Apple ID signed in (for the menu bar app)

---

### Step 1 — Clone the repo

```bash
git clone https://github.com/hcp-uw/mosaic.git
cd mosaic
```

---

### Step 2 — Build and install the CLI + daemon

```bash
chmod +x install.sh
./install.sh
```

This builds two binaries and installs them:

| Binary | Location | Purpose |
|--------|----------|---------|
| `mos` | `/usr/local/bin/mos` | CLI for uploading, downloading, managing files |
| `mosaic-node` | `/usr/local/bin/mosaic-node` | Background daemon — handles network operations |

The daemon starts automatically after install. You can verify it's running:

```bash
mos status node
```

To stop it:

```bash
mos shutdown
```

---

### Step 3 — Build and run the menu bar app

1. Open `MosaicApp/Mosaic.xcodeproj` in Xcode
2. In the project navigator, select the **Mosaic** target
3. Under **Signing & Capabilities**, set your Apple ID as the development team
4. Do the same for the **MosaicFinderSync** target
5. Press **⌘R** to build and run

The Mosaic icon (a drive connected to a line) will appear in your menu bar.

> The app launches the daemon automatically if it's not already running. If you installed via `install.sh` in Step 2, the daemon will already be running and the app will connect to it.

---

### Step 4 — Enable the Finder extension

The Finder extension adds sync badges to files in `~/Mosaic/`. You need to enable it once:

1. Open **System Settings** → **Privacy & Security**
2. Scroll down to **Extensions** → **Added Extensions**
3. Check the box next to **MosaicFinderSync**

If you don't see it listed, make sure you've run the app at least once from Xcode first.

---

### Step 5 — Create the Mosaic folder

The daemon creates `~/Mosaic/` automatically on first launch. You can verify:

```bash
ls ~/Mosaic/
```

This is the folder you'll interact with. Files on the network appear here as stubs or cached copies.

---

## Using Mosaic

### Upload a file

```bash
mos upload /path/to/notes.md
```

This sends the file to the network and creates `~/Mosaic/notes.md.mosaic` — a small stub that represents the remote file in Finder. The status bar menu will show the file with its size and date.

### Open / download a file

**Double-click the stub** in Finder. Mosaic fetches the file from the network and saves it to `~/Mosaic/notes.md`. Depending on your preference (set in the menu bar), it may also open the file automatically.

Or from the **status bar menu**, click the file name and choose:
- **Cache** — download only, don't open
- **Cache & Open** — download and open immediately

### Delete a file

Delete it from Finder normally (⌘Delete or drag to Trash). The daemon detects the deletion and removes the file from the network.

### Rename a file

Rename it in Finder. The daemon detects the rename and updates it on the network.

### Check what's on the network

```bash
mos list
```

Or open the status bar menu — every file on the network is listed with its size, date, and whether it's cached locally.

---

## CLI reference

```
mos upload <file>          Upload a file to the network
mos download <file>        Download a file from the network
mos delete <file>          Delete a file from the network
mos list                   List all files on the network
mos info <file>            Show file metadata
mos status node            Show node status
mos status network         Show network status
mos status account         Show account status
mos peers                  List connected peers
mos join <address>         Join a network node
mos leave                  Leave the network
mos set storage <GB>       Set how much storage to contribute
mos empty storage          Clear contributed storage
mos login key <key>        Log in with a key
mos logout                 Log out
mos shutdown               Stop the daemon
mos version                Show version
mos help                   Show all commands
```

---

## Architecture overview

```
User (Finder / Terminal)
        │
        ▼
  ~/Mosaic/                  ← files and stubs visible to user
        │
   ┌────┴────┐
   │         │
   ▼         ▼
Finder    Menu bar app (Swift)
badges    ├── reads manifest for file list
          ├── intercepts stub double-clicks
          └── HTTP → localhost:7777
                  │
                  ▼
          mosaic-node (Go daemon)
          ├── HTTP :7777        ← Swift app
          ├── Unix socket       ← mos CLI
          ├── Dir watcher       ← syncs Finder actions to network
          └── Manifest          ← ~/.mosaic-manifest.json
                  │
                  ▼
          Peer network (Reed-Solomon shards)
```

See [`MosaicApp/README.md`](MosaicApp/README.md) for a detailed breakdown of the Swift app, and [`internal/daemon/README.md`](internal/daemon/README.md) for the daemon internals.

---

## Troubleshooting

**Menu bar icon doesn't appear**
Make sure the app is running from Xcode (⌘R) and that signing is configured for your Apple ID in both targets.

**Fetch/upload does nothing**
The daemon isn't running or wasn't found. Run `mos status node` in Terminal. If it fails, run `./install.sh` again or start it manually:
```bash
mosaic-node
```

**Finder badges don't show**
The Finder extension isn't enabled. Go to System Settings → Privacy & Security → Extensions → Added Extensions and check MosaicFinderSync. You may need to restart Finder:
```bash
killall Finder
```

**File shows wrong size (Unknown)**
The stub was created before size tracking was added. Delete the stub and re-upload the file:
```bash
mos delete notes.md
mos upload /path/to/notes.md
```

**Daemon logs**
```bash
tail -f /tmp/mosaicd.log
```
