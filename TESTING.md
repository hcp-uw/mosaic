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

## 2. Pull on both remote machines

SSH in and update each machine to match the branch:

```sh
# node1 (relay)
ssh linuxuser@45.32.226.71 'cd ~/mosaic && git fetch origin && git checkout working_demo && git reset --hard origin/working_demo'

# node2 (client)
ssh linuxuser@149.28.13.244 'cd ~/mosaic && git fetch origin && git checkout working_demo && git reset --hard origin/working_demo'
```

## 3. Start the relay on node1

```sh
ssh linuxuser@45.32.226.71
cd ~/mosaic
go run ./relay -addr :9000
```

> Port `9000` must be open in the firewall on node1: `sudo ufw allow 9000/tcp`.

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

### Scripted (non-interactive) test

Send one message and exit; have the other side listen for a fixed time:

```sh
# listener
go run ./client -relay 45.32.226.71:9000 -wait 15s

# sender
go run ./client -relay 45.32.226.71:9000 -msg "hello via relay" -wait 3s
```
