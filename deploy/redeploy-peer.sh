#!/usr/bin/env bash
# Pull the latest working_demo, rebuild the client binary, and restart the
# mosaic-peer systemd service. Run this on a peer node (e.g. node2) after
# pushing code changes.
#
#   ssh linuxuser@149.28.13.244 '~/mosaic/deploy/redeploy-peer.sh'
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root (~/mosaic)

echo "==> Updating to origin/working_demo"
git fetch origin
git checkout working_demo
git reset --hard origin/working_demo

echo "==> Building client -> bin/client"
go build -o bin/client ./client

echo "==> Restarting mosaic-peer"
sudo systemctl restart mosaic-peer

echo "==> Status"
systemctl --no-pager --lines=0 status mosaic-peer
