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
| `GET` | `/upload-progress` | Shard dispatch counts for the active upload |
| `GET` | `/download-progress` | Shard fetch counts for the active download |
| `GET` | `/active-op` | Current in-flight operation, or `null` when idle |

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
| Delete cached `notes.md` | `REMOVE notes.md` | Network delete (after 500ms window) |
| Delete stub `notes.md.mosaic` | `REMOVE notes.md.mosaic` | Full network delete (same as deleting the cached file) |
| Rename `notes.md` → `notes_v2.md` | `RENAME notes.md` + `CREATE notes_v2.md` within 500ms | Network rename + meta.json updated |
| Rename stub `notes.md.mosaic` → `notes_v2.md.mosaic` | `RENAME` + `CREATE` within 500ms | Network rename (same as cached rename) |
| Move `notes.md` out of `~/Mosaic/` | `RENAME notes.md` + no `CREATE` within 500ms | Network delete |
| Drag a new file into `~/Mosaic/` | `CREATE newfile.txt` + no prior `RENAME` within 500ms | Upload to network |
| Undo a delete (Cmd+Z) | `REMOVE notes.md` then `CREATE notes.md` (same name) | Manifest entry restored |
| Copy `notes.md` (Cmd+C / Cmd+V) | `CREATE notes.md copy` | Upload to network (treated as new file) |

**Rename detection:** macOS fires events in either order — `RENAME`+`CREATE` or `CREATE`+`RENAME`. The watcher handles both:
- `RENAME` arrives first: the old path is parked in the `disappeared` map with a 500ms timer. If a `CREATE` arrives before the timer fires, the pair is matched as a rename. If no `CREATE` arrives, the action is a move-out or delete.
- `CREATE` arrives first: it is parked in `recentCreates` for 500ms. If a `RENAME` arrives claiming a manifest entry, the pair is matched as a rename. If no `RENAME` arrives and the file is not in the manifest, it is treated as a drag-and-drop upload.

The CREATE-pair check runs before the `Cached` check, so stub renames (`.mosaic` files) are correctly detected as renames rather than stub deletions.

**Stub deletions:** When the user removes a `.mosaic` stub, the watcher treats it as a full network delete — the same path as deleting a cached file. The network manifest is updated, peers are notified, and all shard holders delete their copies. To remove only the local stub without touching the network, use `mos delete file -s` explicitly.

**Rename + meta.json:** When a rename is detected and confirmed, `RenameFile` handler updates the shard metadata (`meta.json` inside `.shards/<hash>/`) so that `StreamShardToPeer` and `FetchFileBytes` serve the file under the new name.

**Suppression:** Daemon-initiated operations (e.g. deleting a stub after a fetch, writing rename events) call `SuppressNext(path)` before touching the file. This prevents the watcher from misinterpreting its own actions as user actions. Suppression auto-expires after 500ms if the event never fires.

**Ignored events:** `WRITE` and `CHMOD` events are silently dropped — they fire on every chunk written during reconstruction and would cause massive noise. Hidden files (names starting with `.`) are also always ignored — this covers the manifest, temp files, and macOS metadata.

---

## Active operation serialization

Heavy network operations — upload and download — are serialized through a global mutex in `internal/daemon/handlers/activeOp.go`. Only one such operation can run at a time. This prevents concurrent transfers from flooding the UDP send buffer, which caused OS-level "no buffer space available" crashes when redistribution and upload ran simultaneously.

### How it works

**Daemon side (`activeOp.go`):**

```
TryAcquireOp(kind, description) bool  // acquire if idle; returns false if busy
GetActiveOp() *ActiveOp               // returns current op or nil
ReleaseOp()                           // clear when done
```

`upload file` and `download file` handlers call `TryAcquireOp` at the start and `defer ReleaseOp()` at the end. If they can't acquire, they return a response with `"busy": true` and a human-readable `"busyWith"` field so the CLI can report a meaningful error.

**CLI side (`CLI.go`):**

`waitForActiveOp()` polls `GET /active-op` every 500ms and blocks until the response is `null`. It prints a "Waiting: <description>..." status line while blocking, then clears it when the op finishes.

### Which commands wait

| Command | Behaviour |
|---------|-----------|
| `mos upload file` | acquires the lock — is the heavy op |
| `mos download file` | acquires the lock — is the heavy op |
| `mos delete file` | waits — would delete shards mid-upload |
| `mos rename file` | waits — broadcasts manifest mid-transfer is unsafe |
| `mos status node` | waits — broadcasts identity challenge to all peers |
| `mos empty storage` | waits — broadcasts manifest deletion |
| `mos leave network` | waits — disconnecting peers mid-transfer corrupts the transfer |
| `mos logout` | waits — same as leave + deletes shards |
| `mos status network`, `mos list`, `mos status account` | no wait — local reads only |
| `mos join network` | no wait — has its own `IsJoinSettling()` guard |
| `mos wipe`, `mos shutdown` | no wait — lifecycle commands that must proceed |

### HTTP endpoint

`GET /active-op` returns the current operation as JSON or `null`:

```json
{ "kind": "upload", "description": "Uploading notes.md", "startedAt": "..." }
```

The Swift menu bar app can use this to show a progress indicator or disable fetch buttons while a transfer is running.

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
