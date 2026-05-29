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
    if [[ "$1" == -* ]]; then
        echo "deploy.sh does not accept flags. Usage: ./scripts/deploy.sh [droplet-ip]"
        exit 1
    fi
    DROPLET_IP="$1"
else
    DROPLET_IP=$(grep 'DefaultServerIP = ' "$PATHS_FILE" | grep -oE '"[^"]+"' | tr -d '"')
fi

if [ -z "$DROPLET_IP" ]; then
    echo "Could not determine droplet IP from ${PATHS_FILE}."
    echo "Usage: ./deploy.sh [droplet-ip]"
    exit 1
fi

SSH_KEY="${HOME}/.ssh/mosaic-droplet"
REMOTE_USER="root"
REMOTE_DIR="/root/mosaic"
SSH_CTL="${HOME}/.ssh/mosaic-deploy-ctl"

# Shared SSH options: key + ControlMaster so all three connections
# (rsync, build, version publish) share one authenticated session.
SSH_OPTS="-i ${SSH_KEY} -o ControlMaster=auto -o ControlPath=${SSH_CTL} -o ControlPersist=60s"

# Open the master connection now (prompts once for passphrase/password).
ssh ${SSH_OPTS} -o BatchMode=no -fN "${REMOTE_USER}@${DROPLET_IP}"
trap "ssh -o ControlPath=${SSH_CTL} -O exit '${REMOTE_USER}@${DROPLET_IP}' 2>/dev/null; rm -f '${SSH_CTL}'" EXIT

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
    -e "ssh ${SSH_OPTS}" \
    . "${REMOTE_USER}@${DROPLET_IP}:${REMOTE_DIR}/"

echo "✓ Code synced"
echo ""

# Build all server binaries on the droplet
echo "Building on server..."
ssh ${SSH_OPTS} "${REMOTE_USER}@${DROPLET_IP}" bash << 'EOF'
cd /root/mosaic
export PATH=$PATH:/usr/local/go/bin
go build -o bin/mosaic-stun ./cmd/mosaic-stun/
go build -o bin/mosaic-turn ./cmd/mosaic-turn/
go build -o bin/mosaic-version-server ./cmd/mosaic-version-server/
echo "✓ Build complete"
EOF

# Publish current version so `mos version` update checks reflect this deploy.
CURRENT_VERSION=$(grep 'Version = ' internal/version/version.go | sed 's/.*Version = "\(.*\)".*/\1/')
echo "Publishing version ${CURRENT_VERSION}..."
ssh ${SSH_OPTS} "${REMOTE_USER}@${DROPLET_IP}" \
    "mkdir -p /var/run/mosaic && echo '${CURRENT_VERSION}' > /var/run/mosaic/latest-version"
echo "✓ Version ${CURRENT_VERSION} published"

echo ""
echo "Done. To start the servers:"
echo "  ssh -i ~/.ssh/mosaic-droplet root@${DROPLET_IP}"
echo "  cd /root/mosaic && ./scripts/start.sh ${DROPLET_IP}"
