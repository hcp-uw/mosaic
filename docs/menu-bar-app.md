# MosaicApp

The macOS application layer for Mosaic. It runs as a menu bar app (no Dock icon) and gives users a visual interface to the Mosaic network — browsing files, caching them locally, and managing their network presence.

---

## What it does

Mosaic lets you store files across a peer-to-peer network. Instead of uploading to a central server, your files are distributed as shards across trusted nodes. The app makes this feel like a normal folder on your Mac.

When a file is on the network but not downloaded, it appears as a `.mosaic` stub file in `~/Mosaic/`. Double-clicking it (or using the menu) fetches the real file. Once cached, the stub is replaced by the actual file — it looks just like any other file in Finder.

---

## Components

### Mosaic (menu bar app)
The main process. Runs in the background with no Dock icon — accessible only from the status bar icon at the top of your screen.

- Reads `~/Mosaic/.mosaic-manifest.json` to know which files exist on the network
- Talks to the Go daemon (`mosaic-node`) over HTTP on `localhost:7777`
- Intercepts double-clicks on `.mosaic` stub files via `NSDocumentController`
- Watches `~/Mosaic/` for filesystem changes to keep the menu up to date

### MosaicFinderSync (Finder extension)
Runs as a separate process embedded in the app. Adds visual badges to files in `~/Mosaic/` in Finder:
- **On Network** (green badge) — file is cached locally
- **Remote Only** (grey badge) — file exists on the network but is not downloaded

The extension must be enabled in System Settings → Privacy & Security → Extensions → Added Extensions.

### mosaic-node (Go daemon)
The backend process that handles all network operations. The app launches it automatically on startup. It exposes an HTTP API on `localhost:7777` and a Unix socket at `/tmp/mosaicd.sock` for the CLI.

---

## The ~/Mosaic/ folder

This is the folder you interact with. Everything in it is either:

| Type | Looks like | Meaning |
|------|-----------|---------|
| Stub | `notes.md.mosaic` | File is on the network, not downloaded |
| Cached | `notes.md` | File is downloaded and available locally |

You interact with these files exactly like normal files — open, rename, delete. The daemon watches the folder and reflects your actions on the network automatically.

---

## User guide

### Uploading a file

Use the CLI from Terminal:

```bash
mos upload /path/to/notes.md
```

This sends the file to the network and creates `~/Mosaic/notes.md.mosaic` — a small placeholder that appears in Finder. The stub shows the file's original size and date in the Mosaic menu.

### Downloading / opening a file

**Double-click the stub** in Finder. The app fetches the file from the network, saves it to `~/Mosaic/notes.md`, and (depending on your preference) opens it automatically.

Or use the **status bar menu** — click the Mosaic icon, find the file, and choose:
- **Cache** — downloads to `~/Mosaic/` without opening
- **Cache & Open** — downloads and opens immediately

Once cached, double-clicking opens it instantly with no network round-trip.

### Opening a cached file

Just double-click it in Finder like any normal file, or choose **Open** from the status bar submenu.

### Re-fetching a file

If you want to pull a fresh copy from the network (e.g. a peer updated the file):

```bash
mos download notes.md
```

Or use **Re-fetch** from the file's submenu in the status bar.

### Deleting a file

Delete it from Finder — drag to Trash, press ⌘Delete, or right-click → Move to Trash.

The daemon detects the deletion and removes the file from the network. This works for both stubs and cached files.

### Renaming a file

Rename it in Finder as you normally would. The daemon detects the rename and updates the file's name on the network.

### Moving a file out of ~/Mosaic/

Moving a file out of `~/Mosaic/` (dragging it to another folder, or using the CLI `mv`) is treated as a **delete from the network**. The file will no longer be tracked by Mosaic.

Moving a file into `~/Mosaic/` does **not** automatically upload it — use `mos upload` to explicitly add a file to the network.

### Copying a file

Copying a file (⌘C / ⌘V in Finder, or `cp` in Terminal) is ignored by Mosaic. The copy is not uploaded or tracked. Use `mos upload` to explicitly add a new file.

---

## Status bar menu

Click the Mosaic icon (looks like a drive connected to a network) in the menu bar.

```
Mosaic
───────────────────────────
☑ Open files after fetching     ← global toggle
───────────────────────────
notes.md                        ← each network file
  Size: 4.0 KB
  Added: 04-08-2026
  Cached locally: Yes
  ───────────
  Open
  Re-fetch
photo.jpg
  Size: 2.0 MB
  Added: 04-07-2026
  Cached locally: No
  ───────────
  Cache
  Cache & Open
───────────────────────────
Quit Mosaic
```

**Open files after fetching** — when checked, double-clicking a stub (or choosing Cache & Open) opens the file automatically after downloading. When unchecked, fetching only saves the file to disk without opening anything.

---

## Architecture

```
User (Finder / CLI)
        │
        ▼
  ~/Mosaic/               ← files and stubs visible to user
        │
        ▼
  MosaicApp               ← menu bar app (this project)
  ├── AppDelegate          intercepts stub opens, builds menu
  ├── DaemonClient         HTTP calls to localhost:7777
  └── MosaicFinderSync     Finder badges
        │
        ▼
  mosaic-node (Go daemon)
  ├── HTTP server :7777    used by Swift app
  ├── Unix socket          used by mos CLI
  ├── Dir watcher          syncs Finder actions → network
  └── Manifest             tracks all network files
        │
        ▼
  Peer network             (Reed-Solomon shards across nodes)
```

---

## Building & running

Open `MosaicApp/Mosaic.xcodeproj` in Xcode and press ▶. The app will launch in the menu bar.

For the daemon to work, build the Go backend separately:

```bash
# From the repo root
go build -o bin/mosaic-node ./cmd/mosaic-node

# Or install system-wide so the app finds it automatically
sudo cp bin/mosaic-node /usr/local/bin/mosaic-node
```

If no daemon binary is found, the menu bar app still launches and shows the file list (read from the manifest directly), but fetch/upload operations won't work.
