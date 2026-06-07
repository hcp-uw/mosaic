# Scripts

All scripts live in `scripts/`. Run them from the repo root unless noted otherwise.

---

## `install.sh` — end-user installation

Builds and installs the `mos` CLI and `mosaicd` daemon on the **local machine** (Mac, Linux, or Windows). Detects the OS, builds both binaries, copies them to `~/.local/bin/`, and starts the daemon.

```bash
./scripts/install.sh          # full install
./scripts/install.sh --stop   # stop daemon + menu bar app
./scripts/install.sh --wipe   # wipe all local state (prompts for confirmation)
./scripts/install.sh --debug  # show daemon/socket/log diagnostics
```

**What it does (full install):**
1. Detects OS and sets platform-specific paths
2. Stops any running `mosaicd` and the Mosaic menu bar app (macOS)
3. Builds `bin/mos` and `bin/mosaicd` via `go build`
4. Copies binaries to `~/.local/bin/` and marks them executable
5. Starts `mosaicd` in the background; tails the log on failure
6. Builds `MosaicApp/Mosaic.xcodeproj` via `xcodebuild` (macOS only, skipped if Xcode not installed)
7. Launches the menu bar app (macOS only)
8. Prints next-step instructions

**`--wipe`** prompts you to type `"wipe"` before doing anything. If `mos` is already installed it delegates to `mos wipe` (which gracefully leaves the network first); otherwise it manually removes `~/Mosaic/`, `~/.mosaic-*` key/session files, and daemon runtime files.

---

## `deploy.sh` — push code to the droplet

Syncs the local repo to the DigitalOcean droplet over SSH and builds the server binaries there. Run this from your Mac after making changes.

```bash
./scripts/deploy.sh               # reads IP from internal/cli/shared/paths.go
./scripts/deploy.sh 1.2.3.4       # explicit droplet IP
```

**What it does:**
1. Reads the droplet IP from `DefaultServerIP` in `paths.go` (or uses the argument)
2. `rsync`s the repo to `root@<IP>:/root/mosaic/` (excludes `.git`, `bin/`, logs, pids, `files/`, `output/`)
3. SSHes in and runs `go build` for `mosaic-stun` and `mosaic-turn`

Uses the SSH key at `~/.ssh/mosaic-droplet`.

---

## `start.sh` — start servers on the droplet

Starts the STUN and TURN servers. Optionally starts a local `mosaicd` with a forced transport mode for testing.

```bash
./scripts/start.sh                    # auto-detect IP from paths.go
./scripts/start.sh 1.2.3.4            # explicit public IP for TURN
./scripts/start.sh -quic              # also start mosaicd forcing QUIC-only transfers
./scripts/start.sh -udp               # also start mosaicd forcing UDP-only transfers
./scripts/start.sh 1.2.3.4 -quic      # flags and IP can be in any order
```

**What it does:**
1. Parses flags and positional args in any order
2. Builds `mosaic-stun` and `mosaic-turn` if binaries are missing
3. On Linux (non-root): grants `cap_net_bind_service` to `mosaic-stun` so it can bind port 443; prints a `sudo setcap` fix if that fails
4. Starts STUN on port `3478` (UDP) + TCP relay on port `443` (TLS), skips if already running
5. Starts TURN on port `3479` (UDP) with `-public-ip`, skips if already running
6. If `-quic` or `-udp` is passed: kills any existing `mosaicd`, removes stale PID/socket files, builds `mosaicd`, and starts it with `MOSAIC_TRANSPORT=quic|udp`

PID files go to `/var/run/mosaic/`; logs go to `/var/log/mosaic/`.

The `-quic`/`-udp` flags are **dev/testing only** — production clients use auto transport negotiation. When `MOSAIC_TRANSPORT=quic` is set, `mosaicd` will fail shard transfers if QUIC is unavailable rather than falling back to UDP.

---

## `status.sh` — check server status on the droplet

Prints whether each server is running and shows the last 3 log lines.

```bash
./scripts/status.sh
```

Reads PID files from `/var/run/mosaic/` and checks whether the process is alive. Output example:

```
Mosaic server status:
  ✓ STUN server running (PID 1234, port 3478)
  ✓ TURN server running (PID 5678, port 3479)

Recent logs:
  --- stun (last 3 lines) ---
  ...
  --- turn (last 3 lines) ---
  ...
```

---

## `stop.sh` — stop servers on the droplet

Gracefully stops the STUN and TURN servers and removes their PID files.

```bash
./scripts/stop.sh
```

Sends `SIGTERM` to each PID in `/var/run/mosaic/`. Reports whether each server was running or had a stale PID file.
