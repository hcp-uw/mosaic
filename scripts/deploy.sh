#!/usr/bin/env bash
# deploy.sh — push code to the DigitalOcean droplet and build all server binaries.
# Run from the repo root on your Mac:
#   ./deploy.sh [droplet-ip]
#
# If no IP is given, the address is read from internal/cli/shared/paths.go
# (DefaultSTUNServer). Override by passing an explicit IP as the first argument.

set -e

PATHS_FILE="internal/cli/shared/paths.go"

# Extract the IP from DefaultServerIP = "..." if no arg provided.
if [ -n "${1:-}" ]; then
    DROPLET_IP="$1"
else
    DROPLET_IP=$(grep 'DefaultServerIP = ' "$PATHS_FILE" | grep -oE '"[^"]+"' | tr -d '"')
fi

SSH_KEY="${HOME}/.ssh/mosaic-droplet"
REMOTE_USER="root"
REMOTE_DIR="/root/mosaic"

if [ -z "$DROPLET_IP" ]; then
    echo "Could not determine droplet IP from ${PATHS_FILE}."
    echo "Usage: ./deploy.sh [droplet-ip]"
    exit 1
fi

echo "Deploying to ${REMOTE_USER}@${DROPLET_IP}:${REMOTE_DIR}"
echo ""

# Sync code to droplet (exclude generated/secret files)
echo "Syncing code..."
rsync -az --delete \
    --exclude='.git' \
    --exclude='bin/' \
    --exclude='*.log' \
    --exclude='*.pid' \
    --exclude='files/' \
    --exclude='output/' \
    -e "ssh -i ${SSH_KEY}" \
    . "${REMOTE_USER}@${DROPLET_IP}:${REMOTE_DIR}/"

echo "✓ Code synced"
echo ""

# Build all server binaries on the droplet
echo "Building on server..."
ssh -i "${SSH_KEY}" "${REMOTE_USER}@${DROPLET_IP}" bash << 'EOF'
cd /root/mosaic
export PATH=$PATH:/usr/local/go/bin
go build -o bin/mosaic-stun ./cmd/mosaic-stun/
go build -o bin/mosaic-turn ./cmd/mosaic-turn/
echo "✓ Build complete"
EOF

echo ""
echo "Done. To start the servers:"
echo "  ssh -i ~/.ssh/mosaic-droplet root@${DROPLET_IP}"
echo "  cd /root/mosaic && ./scripts/start.sh ${DROPLET_IP}"
