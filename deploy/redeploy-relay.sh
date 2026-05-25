#!/usr/bin/env bash
# Pull the latest working_demo, rebuild the relay binary, and restart the
# systemd service. Run this on node1 after pushing code changes.
#
#   ssh linuxuser@45.32.226.71 '~/mosaic/deploy/redeploy-relay.sh'
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root (~/mosaic)

echo "==> Updating to origin/working_demo"
git fetch origin
git checkout working_demo
git reset --hard origin/working_demo

echo "==> Building relay -> bin/relay"
go build -o bin/relay ./relay

echo "==> Restarting mosaic-relay"
sudo systemctl restart mosaic-relay

echo "==> Status"
systemctl --no-pager --lines=0 status mosaic-relay
