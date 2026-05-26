# Testing the Relay

The **relay runs on node1 (`45.32.226.71`)**, listening on port `9000`.

The clients are **this computer** and **node2 (`149.28.13.244`)** — they connect
to node1 and never talk to each other directly.

| Role   | Host             | Directory      |
| ------ | ---------------- | -------------- |
| relay  | node1 `45.32.226.71`  | `~/mosaic`     |
| client | node2 `149.28.13.244` | `~/mosaic`     |
| client | this computer    | repo root (`mosaic_working_demo`) |

## 1. Push your changes (from this computer)

Run from the repo root:

```sh
git add -A
git commit -m "your message"
git push origin working_demo
```

## 2. Pull on node2 (client)

node1 is updated by the redeploy script in step 3, so it doesn't need a manual
pull here. Update node2 to match the branch:

```sh
ssh linuxuser@149.28.13.244 'cd ~/mosaic && git fetch origin && git checkout working_demo && git reset --hard origin/working_demo'
```

## 3. Run the relay on node1

The relay runs as a systemd service (`mosaic-relay`) so it survives logout and
restarts on crash. After pushing code changes, redeploy it in one command:

```sh
ssh linuxuser@45.32.226.71 '~/mosaic/deploy/redeploy-relay.sh'
```

This pulls `working_demo`, rebuilds `bin/relay`, and restarts the service.
Useful commands:

```sh
sudo systemctl status mosaic-relay      # is it running?
journalctl -u mosaic-relay -f           # follow relay logs
sudo systemctl restart mosaic-relay     # restart without rebuilding
```

> Port `9000` must be open in the firewall on node1: `sudo ufw allow 9000/tcp`.

### First-time install

The service is already installed and enabled on node1, so you do not need to run
these steps for normal testing — they are recorded here only for reprovisioning a
fresh machine:

```sh
sudo cp ~/mosaic/deploy/mosaic-relay.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable mosaic-relay
~/mosaic/deploy/redeploy-relay.sh
```

For an ad-hoc foreground run instead (no service):

```sh
cd ~/mosaic && go run ./relay -addr :9000
```

## 4. Run the clients

On node2:

```sh
ssh linuxuser@149.28.13.244
cd ~/mosaic
go run ./client -relay 45.32.226.71:9000
```

On this computer (from the repo root):

```sh
go run ./client -relay 45.32.226.71:9000
```

Then type messages; they are forwarded through the relay to the other client.
Clients also accept RPC commands:

```sh
store_shard <address> <data>     # persist a shard on the other clients
retrieve_shard <address>         # fetch a shard's data from the other clients
```

### Reset shard storage (for reproducible tests)

Stored shards persist under `~/Mosaic/.shards` on each machine that runs a
client. Clear them before a fresh run so old shards don't affect results.

```sh
# this computer
rm -rf ~/Mosaic/.shards

# node2 (client)
ssh linuxuser@149.28.13.244 'rm -rf ~/Mosaic/.shards'
```

> The relay (node1) stores nothing, so it needs no reset.

### Scripted (non-interactive) test

Send one message and exit; have the other side listen for a fixed time:

```sh
# listener
go run ./client -relay 45.32.226.71:9000 -wait 15s

# sender
go run ./client -relay 45.32.226.71:9000 -msg "hello via relay" -wait 3s
```

## 5. File sharding (drop-to-shard, double-click-to-open)

A file dropped into `~/Mosaic` is **Reed-Solomon encoded into 14 shards (10 data
+ 4 parity)**, **distributed across the connected nodes** (round-robin — each
shard goes to one node, not replicated to all), and replaced by a small
`<name>.mosaic` stub (a JSON manifest). Opening the stub downloads the shards and
reconstructs the original file. Any **10 of the 14** shards are enough, so up to
**4 lost shards** are tolerated.

Shard addresses are `<sha256-of-file>.<NN>` (content digest + index `00`–`13`);
indices `00`–`09` are data shards and `10`–`13` parity. The manifest records the
data/parity split so rehydrate can reconstruct from whatever subset survives.

Every connected client is a **node**: it serves `store_shard`/`retrieve_shard`
for the network *and*, when run with `-node`, watches its own `~/Mosaic` and
shards files dropped into it. There is no separate "peer" vs "watcher" — same
program, same mode. Shards live only on the *other* nodes (the relay never
echoes to the sender), so you need at least one other node connected to hold
them.

Both **node1** (alongside the relay) and **node2** run a `mosaic-node` service,
so by default there are two storage nodes — a sharded file ends up ~7 shards on
each. With N nodes, shard `i` is stored on node `i mod N`.

> Distribution uses node discovery: a client broadcasts a `ping` and collects
> the addresses that answer. The interactive `nodes` command prints the count:
>
> ```
> nodes
> [nodes] 3 connected (this one + 2 other):
>   - 45.32.226.71:... (node1)
>   - 149.28.13.244:... (node2)
> ```
>
> Redundancy comes from the parity shards, not replication: any 10 of 14 shards
> reconstruct the file. Note this is *shard*-level tolerance — with only 2 nodes
> (~7 shards each), losing a whole node loses 7 shards and the file can't be
> rebuilt. To survive a full node loss you need enough nodes that no node holds
> more than 4 shards (e.g. 4+ nodes).

**Storage nodes run as `mosaic-node` services** on both node1 and node2 — each
connects to the relay and serves shards (and watches its own `~/Mosaic`), so
they are always available. node1 runs both `mosaic-relay` and `mosaic-node`.
After pushing code, redeploy each:

```sh
ssh linuxuser@45.32.226.71  '~/mosaic/deploy/redeploy-node.sh'   # node1
ssh linuxuser@149.28.13.244 '~/mosaic/deploy/redeploy-node.sh'   # node2

sudo systemctl status mosaic-node     # is it up?
journalctl -u mosaic-node -f          # follow node logs
```

> First-time install (already done on node1 and node2):
>
> ```sh
> sudo cp ~/mosaic/deploy/mosaic-node.service /etc/systemd/system/
> sudo systemctl daemon-reload
> sudo systemctl enable mosaic-node
> ~/mosaic/deploy/redeploy-node.sh
> ```
>
> For an ad-hoc node instead of the service:
> `ssh linuxuser@149.28.13.244 'cd ~/mosaic && go run ./client -relay 45.32.226.71:9000 -node'`

**Run a node on this computer** too, so files dropped into `~/Mosaic` get
sharded (and so this machine also serves shards to the network):

```sh
go run ./client -relay 45.32.226.71:9000 -node
```

Now drop a file into `~/Mosaic`; it becomes `<name>.mosaic`. To get it back:

```sh
go run ./client -relay 45.32.226.71:9000 -rehydrate ~/Mosaic/<name>.mosaic -open
```

### Double-click support (macOS)

Install an app that opens `.mosaic` files on double-click:

```sh
deploy/install-mosaic-app.sh 45.32.226.71:9000
```

This builds the client, creates `~/Applications/Mosaic.app` registered as the
handler for `.mosaic`, and points it at the given relay. Double-clicking a
`.mosaic` stub then downloads and opens the original. (First time, you may need
to right-click → Open With → Mosaic → "Always Open With".)

### Reset file-sharing state

In addition to clearing `~/Mosaic/.shards` (above), remove any stubs/files left
in `~/Mosaic` between runs:

```sh
rm -f ~/Mosaic/*.mosaic
```

## Running long-lived processes on the remote nodes

Prefer the systemd services (`mosaic-relay` on node1, `mosaic-node` on node2):
they survive logout and restart on crash. Manage them with
`sudo systemctl {status,restart,stop} <unit>` and `journalctl -u <unit> -f`.

If you must launch something ad-hoc over SSH (not as a service), don't rely on
`cmd &` / `nohup &` / `setsid &` in a one-shot SSH command — the process is
killed when the channel closes (you'll see `exit 255`). Use a detached tmux
session instead, and keep the launch command minimal (no `sleep`/`pgrep`
chained after `tmux new-session`, which also trips the 255):

```sh
ssh linuxuser@149.28.13.244 \
  'tmux new-session -d -s node "/path/to/client -relay 45.32.226.71:9000 -home ~/Mosaic -node >/tmp/node.log 2>&1"'
# verify separately:
ssh linuxuser@149.28.13.244 'tmux ls; cat /tmp/node.log'
```
