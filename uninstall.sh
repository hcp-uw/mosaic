#!/bin/bash

set -e

GREEN='\033[0;32m'
NC='\033[0m'

echo "Stopping daemon..."
if [ -f /tmp/mosaicd.pid ]; then
    kill $(cat /tmp/mosaicd.pid) 2>/dev/null || true
    rm -f /tmp/mosaicd.pid /tmp/mosaicd.sock
    echo -e "${GREEN}✓ Daemon stopped${NC}"
else
    pkill -f mosaicd 2>/dev/null || true
    rm -f /tmp/mosaicd.sock
    echo -e "${GREEN}✓ Daemon stopped${NC}"
fi

echo ""
echo "Uninstalling..."
sudo rm -f /usr/local/bin/mos
sudo rm -f /usr/local/bin/mosaicd
rm -rf bin/
rm -f /tmp/mosaicd.sock /tmp/mosaicd.pid /tmp/mosaicd.log
echo -e "${GREEN}✓ Uninstalled${NC}"