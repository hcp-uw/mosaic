# fileSystem package

This package manages everything that touches `~/Mosaic/` on disk — creating the directory, writing stub files, and maintaining the manifest.

---

## The ~/Mosaic/ directory

`~/Mosaic/` is the folder the user sees in Finder. It contains two kinds of files:

| File | Meaning |
|------|---------|
| `notes.md.mosaic` | Stub — file exists on the network but is not downloaded yet |
| `notes.md` | Real file — downloaded and cached locally |

A file moves from stub → real when the user opens or re-fetches it. Once cached, the stub is deleted and only the real file remains.

---

## Files

### fileSystem.go
Bootstraps the `~/Mosaic/` directory on daemon startup (`StartMount`). Previously this was a FUSE mount; now it's a plain directory.

### stubs.go
Manages `.mosaic` stub files. A stub is a small JSON file that represents a remote-only file in Finder.

**Stub format:**
```json
{
  "name": "notes.md",
  "size": 4096,
  "nodeID": 3,
  "dateAdded": "04-08-2026",
  "cached": false
}
```

Key functions:
- `WriteStub` — creates a `.mosaic` stub when a file is uploaded
- `ReadStub` — reads stub metadata (used as fallback when no manifest entry exists)
- `RemoveStub` — deletes the stub (called on network delete, or after a file is cached)
- `MarkCached` — flips `cached: true` on the stub JSON (legacy — manifest is now authoritative)
- `IsCached` — checks the stub's cached flag (legacy — prefer checking file existence directly)

> Stubs are temporary. Once a file is fetched, the stub is deleted and the manifest becomes the only metadata record.

### manifest.go
The manifest is the authoritative record of every file on the network. Unlike stubs, it persists even after a file is cached locally (when the stub is gone).

**Location:** `~/Mosaic/.mosaic-manifest.json`

**Format:**
```json
{
  "notes.md": {
    "name": "notes.md",
    "size": 4096,
    "nodeID": 3,
    "dateAdded": "04-08-2026",
    "cached": true
  },
  "photo.jpg": {
    "name": "photo.jpg",
    "size": 2097152,
    "nodeID": 1,
    "dateAdded": "04-07-2026",
    "cached": false
  }
}
```

Key functions:
- `AddToManifest` — called on upload; creates the entry with correct size and date
- `RemoveFromManifest` — called on network delete
- `RenameInManifest` — called when the watcher detects a rename in Finder
- `MarkCachedInManifest` — called after a fetch; marks the file as locally cached
- `IsInManifest` — used by the watcher to distinguish Mosaic-tracked files from unrelated files
- `ReadManifest` — returns all entries; used by the status bar menu to list files

**Concurrency:** All manifest reads and writes go through a `sync.Mutex`. Writes use an atomic rename pattern — data is written to `.mosaic-manifest.json.tmp` first, then renamed over the real file in a single syscall. This prevents corruption if the daemon crashes mid-write.

---

## File lifecycle

```
mos upload notes.md
  → WriteStub("notes.md")         # creates notes.md.mosaic in ~/Mosaic/
  → AddToManifest("notes.md")     # creates manifest entry

User double-clicks notes.md.mosaic (or opens from status bar)
  → FetchFileBytes()              # downloads the real file
  → writes notes.md to ~/Mosaic/
  → SuppressNext(stub path)       # tells watcher to ignore next event on stub
  → os.Remove(notes.md.mosaic)    # stub deleted — real file takes its place
  → MarkCachedInManifest()        # manifest entry updated

User deletes notes.md from Finder
  → watcher sees REMOVE
  → IsInManifest() → true
  → deleteFromNetwork()           # triggers network delete
  → RemoveFromManifest()          # manifest entry removed
```
