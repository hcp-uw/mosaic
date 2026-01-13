#!/bin/bash

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo "Building binaries..."
mkdir -p bin
go build -o bin/mos ./cmd/mosaic-cli
go build -o bin/mosaicd ./cmd/mosaic-node
echo -e "${GREEN}✓ Build complete!${NC}"

echo ""
echo "Installing to /usr/local/bin..."
sudo cp bin/mos /usr/local/bin/
sudo cp bin/mosaicd /usr/local/bin/
echo -e "${GREEN}✓ Installed successfully!${NC}"

echo ""
echo "Starting daemon..."
if pgrep -f mosaicd > /dev/null; then
    echo "Daemon already running"
else
    mosaicd > /tmp/mosaicd.log 2>&1 &
    echo $! > /tmp/mosaicd.pid
    rm -rf bin/
    echo -e "${GREEN}✓ Daemon started (logs: /tmp/mosaicd.log)${NC}"
fi

echo ""
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}✓ Mosaic is ready!${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "Usage:"
echo "  mos help       - View all mosaic commands"
echo ""
echo "To stop mosaic:"
echo "  mos shutdown   - Stop daemon and cleanup"