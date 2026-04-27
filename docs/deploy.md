# Deploying the Mosaic STUN/TURN Server

Mosaic's public infrastructure is a single server running two processes:

| Process | Port | Purpose |
|---|---|---|
| `mosaic-stun` | 3478 UDP | Peer discovery and hole punching |
| `mosaic-turn` | 3479 UDP | Relay fallback for peers behind strict NAT |

Throughout this guide, replace the following placeholders with your actual values:

| Placeholder | What to substitute |
|---|---|
| `<server-ip>` | Your server's public IP address |
| `<ssh-key>` | Path to your SSH private key (e.g. `~/.ssh/id_ed25519`) |

---

## Prerequisites

### SSH access

Password authentication is disabled on the server — you need an SSH key.

```bash
# Set correct permissions on your key file
chmod 600 <ssh-key>

# Verify you can connect before doing anything else
ssh -i <ssh-key> root@<server-ip>
```

### Go on the server

Go must be installed on the server for remote builds. Check:

```bash
ssh -i <ssh-key> root@<server-ip> 'go version'
```

If missing, install it:

```bash
ssh -i <ssh-key> root@<server-ip> bash << 'EOF'
wget -q https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> /root/.bashrc
EOF
```

---

## Deploying a Code Change

Run from the repo root on your local machine:

```bash
./deploy.sh <server-ip>
```

This does two things:
1. Syncs the source code to `/root/mosaic/` on the server (skips `.git`, `bin/`, logs)
2. Builds `mosaic-stun` and `mosaic-turn` on the server so the binary matches the server's CPU architecture

After deploying, restart the servers to pick up the new binaries:

```bash
ssh -i <ssh-key> root@<server-ip>
cd /root/mosaic
./scripts/stop.sh
./scripts/start.sh <server-ip>
```

---

## Starting and Stopping Servers

Run these commands on the server (after SSH-ing in).

### Start

```bash
./scripts/start.sh <server-ip>
```

Both processes run in the background. PIDs are written to `/var/run/mosaic/` and logs go to `/var/log/mosaic/`.

### Stop

```bash
./scripts/stop.sh
```

Sends SIGTERM to each process and cleans up PID files.

### Restart

```bash
./scripts/stop.sh && ./scripts/start.sh <server-ip>
```

---

## Checking Server Status

### Are the processes running?

```bash
pgrep -fl mosaic
```

Expected output:

```
12345 mosaic-stun
12346 mosaic-turn
```

### Tail the logs

```bash
tail -f /var/log/mosaic/stun.log
tail -f /var/log/mosaic/turn.log
```

### Verify the STUN port is reachable from your local machine

```bash
echo -n "test" | nc -u -w2 <server-ip> 3478
```

A timeout is expected (STUN ignores raw text), but "connection refused" means the port is blocked.

---

## First-Time Server Setup

If you are setting up a brand-new server from scratch:

```bash
# 1. SSH in with your key
ssh -i <ssh-key> root@<server-ip>

# 2. Install Go (see Prerequisites above)

# 3. Create the repo directory
mkdir -p /root/mosaic

# 4. Exit and deploy from your local machine
exit
./deploy.sh <server-ip>

# 5. SSH back in and start the servers
ssh -i <ssh-key> root@<server-ip>
cd /root/mosaic
./scripts/start.sh <server-ip>
```

### Firewall setup (ufw)

Open the required ports and enable the firewall:

```bash
ufw allow 22/tcp     # SSH — do this first so you don't lock yourself out
ufw allow 3478/udp   # STUN
ufw allow 3479/udp   # TURN
ufw enable
ufw status
```

### Harden SSH

Edit `/etc/ssh/sshd_config` and confirm these lines are set:

```
PasswordAuthentication no
PubkeyAuthentication yes
```

Then apply the change:

```bash
systemctl restart ssh
```

---

## Connecting Clients

Once the STUN server is running, clients connect with:

```bash
mos join <server-ip>:3478
```

---

## Troubleshooting

**Cannot SSH into the server**
Make sure your key file permissions are correct (`chmod 600 <ssh-key>`) and the server's firewall allows port 22.

**Deploy fails on rsync**
Confirm SSH access works first:
```bash
ssh -i <ssh-key> root@<server-ip> 'echo ok'
```

**Build fails on the server**
Go may not be in `PATH`. Try:
```bash
ssh -i <ssh-key> root@<server-ip> 'export PATH=$PATH:/usr/local/go/bin && go version'
```

**STUN server not starting**
Check the log for the error message:
```bash
cat /var/log/mosaic/stun.log
```

If the port is already in use (a stale process is running):
```bash
./scripts/stop.sh
pkill -9 -f mosaic-stun
./scripts/start.sh <server-ip>
```

**PID file exists but process is dead**
The stop script cleans up stale PIDs automatically. To do it manually:
```bash
rm -f /var/run/mosaic/*.pid
```
