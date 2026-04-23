# daemon package

The daemon is a long-running background process (`mosaic-node`) that owns all network logic. The Swift menu bar app and CLI are thin clients that talk to it — they contain no network code themselves.

---

## Entry point

`cmd/mosaic-node/main.go` starts three things in order:

1. `filesystem.StartMount` — ensures `~/Mosaic/` exists
2. `daemon.StartDirWatcher` — watches `~/Mosaic/` for filesystem events
3. `daemon.StartHTTPServer` — HTTP API on `localhost:7777` (goroutine)
4. `daemon.StartServer` — Unix socket server (blocks main goroutine)

---

## Files

### server.go — Unix socket server

Listens on a Unix domain socket (path defined in `internal/cli/shared`). The CLI (`mos` command) connects here to send commands.

All communication is JSON. Each request has a `command` field and a `data` payload:
```json
{ "command": "uploadFile", "data": { "path": "/Users/bob/notes.md" } }
```

The server decodes the command, routes it to the appropriate handler in `internal/daemon/handlers/`, and writes back a JSON response.

Supported commands: `joinNetwork`, `uploadFile`, `downloadFile`, `deleteFile`, `listFiles`, `fileInfo`, `getPeers`, `setStorage`, and more — see the switch statement in `server.go` for the full list.

### httpserver.go — HTTP API

Listens on `localhost:7777`. Used by the Swift menu bar app and Finder Sync extension (they can't use Unix sockets easily from Swift).

| Method | Path | What it does |
|--------|------|--------------|
| `GET` | `/files` | List all network files with metadata |
| `GET` | `/files/{name}/info` | Metadata for a single file |
| `DELETE` | `/files/{name}` | Delete file from network |
| `POST` | `/files/{name}/fetch` | Download and cache file locally |

The fetch endpoint (`POST /files/{name}/fetch`) does more than just download:
1. Calls `DownloadFile` handler to fetch bytes and write `~/Mosaic/<name>`
2. If no manifest entry exists (file came from a peer, not uploaded by this node), creates a minimal one
3. Suppresses the watcher so the stub deletion isn't misread as a user delete
4. Deletes the `.mosaic` stub — the real file now lives in its place
5. Marks the manifest entry as cached

### watcher.go — Filesystem event watcher

Watches `~/Mosaic/` using `fsnotify` and maps filesystem events to network operations. This is what makes Finder feel like a first-class interface — deleting or renaming a file in Finder automatically reflects on the network.

**Event mapping:**

| User action in Finder | Events seen | Network result |
|----------------------|-------------|----------------|
| Delete `notes.md` | `REMOVE notes.md` | Network delete |
| Rename `notes.md` → `notes_v2.md` | `RENAME notes.md` + `CREATE notes_v2.md` within 75ms | Network rename |
| Move `notes.md` out of `~/Mosaic/` | `RENAME notes.md` + no `CREATE` within 75ms | Network delete |
| Copy `notes.md` (Cmd+C / Cmd+V) | `CREATE notes.md copy` | Ignored — copy is not an upload |

**Rename detection:** macOS only fires a `RENAME` event on the old path — it doesn't tell you what the file became. The watcher starts a 75ms timer when it sees a `RENAME`. If a `CREATE` arrives inside `~/Mosaic/` before the timer fires, the two events are paired as a rename. If the timer expires with no `CREATE`, the file was moved out of the folder and is treated as a delete.

**Suppression:** Daemon-initiated operations (e.g. deleting a stub after a fetch) call `SuppressNext(path)` before touching the file. This prevents the watcher from misinterpreting its own actions as user actions. Suppression auto-expires after 500ms if the event never fires.

**Ignored paths:** Hidden files (names starting with `.`) are always ignored — this covers the manifest, the `.tmp` atomic write file, and any macOS metadata files.

---

## How the three servers relate

```
CLI (mos upload notes.md)
  └─→ Unix socket → server.go → handlers/uploadFile.go
                                  └─→ writes stub + manifest entry

Swift menu bar app
  └─→ HTTP localhost:7777 → httpserver.go → handlers/...

Finder (user deletes notes.md)
  └─→ fsnotify event → watcher.go → handlers/deleteFile.go
                                       └─→ removes stub + manifest entry
```

All three paths ultimately call the same handlers in `internal/daemon/handlers/`. The handlers are the single source of truth for what "upload", "delete", "fetch", etc. actually mean.
