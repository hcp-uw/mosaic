# Mosaic — Handoff

A small distributed file-sharding demo. Files dropped into `~/Mosaic` are
erasure-coded into shards, scattered across nodes on the network, and replaced
by a tiny stub; opening the stub pulls the shards back and reconstructs the
file. Think "toy Dropbox with Reed-Solomon."

Module: `github.com/hcp-uw/mosaic` · Go 1.24 · branch: **`working_demo`** (work
happens here, not `main`).

---

## Architecture

Three programs, one tiny RPC protocol, all over plain TCP lines.

```
            ┌──────────────┐
            │    relay      │   node1 (45.32.226.71:9000)
            │ (line hub)    │   systemd: mosaic-relay
            └──────┬───────┘
       broadcast / │ directed delivery
        ┌──────────┼───────────┐
        ▼          ▼           ▼
     node (n1)  node (n2)   client (your Mac)
     serves +   serves +    -node / -rehydrate
     stores     stores
```

- **`relay/`** — dumb message hub. Clients connect over TCP; the relay keys them
  by their `IP:port`. By default every line is **broadcast** to all other
  clients, prefixed with the sender's address (`"<addr>: <payload>\n"`). A line
  beginning with the `proto.RoutePrefix` (`"MOSAIC-TO <addr> "`) is instead
  **delivered to that one client**. The relay understands nothing else — no JSON,
  no shard logic.

- **`client/`** — does everything else. One binary, several modes (see below).
  Every connected client is a **node**: it always answers `store_shard` /
  `retrieve_shard` / `ping` for the network. The `-node` flag additionally makes
  it watch its `~/Mosaic` dir and shard files dropped there.

- **`proto/`** — the JSON RPC envelope (`Message`), method names, param/result
  structs, and the relay routing helpers (`Route` / `SplitRoute`).

- **`shardstore/`** — local on-disk shard storage (`<base>/.shards`, one
  base64url-named file per shard). A `Store` type takes a configurable base dir
  so multiple clients can simulate separate nodes on one machine.

### RPC layer (`proto`)

One JSON `Message` per line. Requests carry a unique `ID`, `Method`, `Params`;
responses echo the `ID`/`Method` and carry `Result`. `Message.From` is filled in
on receipt from the relay's line prefix (never serialized). Non-JSON lines are
treated as plain chat text.

Methods:
- `store_shard{address, data}` → `{success, error}`
- `retrieve_shard{address}` → `{found, data, error}` — responders **stay silent
  unless they hold the shard** (so only the one holder answers).
- `ping{}` → `{ok}` — used for node discovery.

`client.call(to, method, params, timeout, stop)` is the workhorse: registers a
response channel by request ID, sends the request (routed to `to` if non-empty,
else broadcast), and collects responses until `stop` matches one or the timeout
fires. Responses are routed **back to the caller** (via `From`) to avoid fan-out.

### File pipeline (`client/files.go`)

- **Shard addressing:** `<sha256-of-file-hex>.<NN>`, index `00`–`13`. Content-
  addressed; `00`–`09` are data shards, `10`–`13` parity.
- **Encode:** Reed-Solomon **10 data + 4 parity = 14** shards
  (`github.com/klauspost/reedsolomon`). Any 10 of 14 reconstruct the file.
- **Distribute:** shard `i` → `nodes[i mod N]` (round-robin), via `ping`
  discovery then directed `store_shard`. **Not replicated** — each shard on one
  node. The sharding client never stores its own shards (relay doesn't echo to
  sender).
- **Stub:** the original is replaced by `<name>.mosaic`, a JSON `manifest`
  (name, size, sha256, data/parity counts, the 14 shard addresses).
- **Rehydrate:** fetch shards (missing ones left nil), need ≥10, `ReconstructData`
  fills gaps from parity, `Join` + sha256 verify, write the file next to the
  stub (stub kept).
- **Watcher:** polls `~/Mosaic` every 1s; shards a file once it's been stable
  for one poll (avoids grabbing mid-copy); skips hidden files, `.mosaic` stubs,
  and any file that already has a `.mosaic` sibling (avoids a re-shard loop on
  rehydrated files).

### Client modes (flags)

- `-node` — run as a network node: serve shards + watch `~/Mosaic`.
- `-rehydrate <stub>` [`-open`] — reconstruct a `.mosaic` stub, then exit.
- (default) — interactive: type `nodes`, `store_shard <addr> <data>`,
  `retrieve_shard <addr>`, or any other line (sent as chat).
- `-msg <line>` / `-wait <dur>` — scripted single-message test mode.
- `-relay host:port` (default `127.0.0.1:9000`), `-home <dir>` (default
  `~/Mosaic`), `-timeout <dur>` (RPC wait, default 4s).

### macOS double-click

`deploy/install-mosaic-app.sh <relay>` builds `~/Applications/Mosaic.app` (an
AppleScript droplet) registered as the handler for `.mosaic`. Double-clicking a
stub runs `client -rehydrate <file> -open`. No FUSE — macFUSE on Apple Silicon
needs a Recovery-mode kext approval, which is why we went the app-handler route.

---

## Deployment (current live setup)

Two Vultr boxes, user `linuxuser`, repo cloned at `~/mosaic`, passwordless sudo.

| Host  | IP              | systemd units                 |
| ----- | --------------- | ----------------------------- |
| node1 | `45.32.226.71`  | `mosaic-relay` + `mosaic-node`|
| node2 | `149.28.13.244` | `mosaic-node`                 |

Services run prebuilt binaries (`bin/relay`, `bin/client`), `Restart=always`,
survive logout. Redeploy after pushing:

```sh
# relay (node1 only):
ssh linuxuser@45.32.226.71  '~/mosaic/deploy/redeploy-relay.sh'
# node (both):
ssh linuxuser@45.32.226.71  '~/mosaic/deploy/redeploy-node.sh'
ssh linuxuser@149.28.13.244 '~/mosaic/deploy/redeploy-node.sh'
```

Manage with `sudo systemctl {status,restart} <unit>` and `journalctl -u <unit> -f`.

> **SSH gotcha:** launching long-lived processes ad-hoc over SSH with `&` /
> `nohup` / `setsid` gets them killed when the channel closes (shows `exit 255`).
> Use the systemd services, or a detached `tmux new-session -d` in a *minimal*
> SSH command (no `sleep`/`pgrep` chained after it). See TESTING.md.

---

## Quick start (local, one machine)

```sh
go build -o /tmp/relay ./relay && go build -o /tmp/client ./client
/tmp/relay -addr 127.0.0.1:9100 &
/tmp/client -relay 127.0.0.1:9100 -home /tmp/n1 -node &   # node 1
/tmp/client -relay 127.0.0.1:9100 -home /tmp/n2 -node &   # node 2
# drop a file in /tmp/me, then run a watcher on it:
mkdir -p /tmp/me && echo hello > /tmp/me/x.txt
/tmp/client -relay 127.0.0.1:9100 -home /tmp/me -node &   # shards x.txt -> x.txt.mosaic
# reconstruct:
/tmp/client -relay 127.0.0.1:9100 -home /tmp/me -rehydrate /tmp/me/x.txt.mosaic
```

`TESTING.md` has the full local + cross-network procedures, reset commands, and
the double-click app install.

---

## Status & verified behavior

Working and tested end-to-end (local and across node1/node2):
- Drop-to-shard, stub creation, rehydrate with byte-identical reconstruction.
- Shards **distributed** 7/7 across the two nodes (not replicated).
- RS fault tolerance: reconstructs with up to **4 shards missing**; fails
  cleanly at 5 missing (`only 9 of 14 shards available; need 10`).
- macOS double-click rehydration (tested via `open -a`).

---

## Known issues / good next tasks

1. **No node-loss tolerance with 2 nodes.** RS tolerates losing any 4 *shards*,
   but each node holds ~7, so losing a whole node loses 7 > 4. Bring up a
   3rd/4th node (just install `mosaic-node`) so no node holds >4 shards.
2. **Slow degraded rehydrate.** A node stays silent when it lacks a shard, so
   each missing shard costs the full RPC timeout (~4s), serially. **Fetch shards
   concurrently** to bound the wait to one timeout.
3. **No re-distribution / repair.** If a node dies, surviving shards aren't
   re-replicated to restore redundancy. No background repair exists.
4. **Discovery is best-effort.** `discoverNodes` waits a fixed 1.5s window; a
   slow node could be missed and miss its share of shards.
5. **`@`-style routing collides with chat.** Chat is a leftover demo feature;
   the `MOSAIC-TO ` route prefix is chosen to avoid collision, but plain chat is
   otherwise unprotected.
6. **`hello.go`** at the repo root is leftover scaffolding (`package main`,
   prints "Hello, World!"), unrelated to relay/client — safe to delete.
7. **Single relay** is a SPOF and unauthenticated/unencrypted (fine for a demo,
   not for anything real).

## Conventions

- Work on `working_demo`. Commit only when asked; keep commits focused.
- Run `go build ./... && go vet ./...` before committing; `gofmt` everything.
- After changing the relay, redeploy `mosaic-relay` on node1; after changing the
  client, redeploy `mosaic-node` on **both** nodes.
- Clean up test artifacts: `rm -f ~/Mosaic/.shards/*` on nodes, `~/Mosaic/*.mosaic`.
