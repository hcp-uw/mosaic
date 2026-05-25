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
