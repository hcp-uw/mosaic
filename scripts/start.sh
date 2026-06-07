#!/usr/bin/env bash
# scripts/start.sh — start STUN and TURN servers on the droplet.
# Run from the repo root on the droplet:
#   ./scripts/start.sh [public-ip]
#
# If no IP is given, it is read from internal/cli/shared/paths.go (DefaultServerIP).

set -e

PATHS_FILE="internal/cli/shared/paths.go"

if [ -n "${1:-}" ]; then
    PUBLIC_IP="$1"
else
    PUBLIC_IP=$(grep 'DefaultServerIP = ' "$PATHS_FILE" | grep -oE '"[^"]+"' | tr -d '"')
fi

if [ -z "$PUBLIC_IP" ]; then
    echo "Could not determine public IP from ${PATHS_FILE}."
    echo "Usage: ./scripts/start.sh [public-ip] [-quic|-udp]"
    exit 1
fi

LOG_DIR="/var/log/mosaic"
PID_DIR="/var/run/mosaic"

mkdir -p "$LOG_DIR" "$PID_DIR" bin

# Build server binaries if missing
if [ ! -f "./bin/mosaic-stun" ] || [ ! -f "./bin/mosaic-turn" ] || [ ! -f "./bin/mosaic-version-server" ]; then
    printf "Building server binaries..."
    if go build -o bin/mosaic-stun ./cmd/mosaic-stun && \
       go build -o bin/mosaic-turn ./cmd/mosaic-turn && \
       go build -o bin/mosaic-version-server ./cmd/mosaic-version-server; then
        echo " ✓"
    else
        echo " ✗"
        echo "Build failed. Run 'go build ./...' for details."
        exit 1
    fi
fi

# The TCP relay embedded in mosaic-stun listens on port 443.
# On Linux, binding ports below 1024 requires either root or the net_bind_service capability.
# Try to grant it automatically; print a clear fix if that fails.
if [ "$(uname -s)" = "Linux" ] && [ "$(id -u)" -ne 0 ]; then
    if command -v getcap > /dev/null 2>&1 && ! getcap ./bin/mosaic-stun 2>/dev/null | grep -q "net_bind_service"; then
        printf "Granting net_bind_service capability to mosaic-stun (needed for port 443)..."
        if setcap 'cap_net_bind_service=+ep' ./bin/mosaic-stun 2>/dev/null; then
            echo " ✓"
        else
            echo ""
            echo "⚠  Could not grant net_bind_service — TCP relay will fail to bind port 443."
            echo "   Fix with one of:"
            echo "     sudo setcap 'cap_net_bind_service=+ep' $(pwd)/bin/mosaic-stun"
            echo "     sudo ./scripts/start.sh $PUBLIC_IP"
        fi
    fi
fi

# STUN server (also runs the TCP relay on port 443)
if [ -f "${PID_DIR}/stun.pid" ] && kill -0 "$(cat ${PID_DIR}/stun.pid)" 2>/dev/null; then
    echo "STUN server already running (PID $(cat ${PID_DIR}/stun.pid))"
else
    ./bin/mosaic-stun > "${LOG_DIR}/stun.log" 2>&1 &
    echo $! > "${PID_DIR}/stun.pid"
    echo "✓ STUN + TCP relay started (PID $!)"
fi

# TURN server
if [ -f "${PID_DIR}/turn.pid" ] && kill -0 "$(cat ${PID_DIR}/turn.pid)" 2>/dev/null; then
    echo "TURN server already running (PID $(cat ${PID_DIR}/turn.pid))"
else
    ./bin/mosaic-turn -public-ip "$PUBLIC_IP" > "${LOG_DIR}/turn.log" 2>&1 &
    echo $! > "${PID_DIR}/turn.pid"
    echo "✓ TURN server started (PID $!)"
fi

# Version server (serves /var/run/mosaic/latest-version over HTTP on port 8080)
if [ -f "${PID_DIR}/version.pid" ] && kill -0 "$(cat ${PID_DIR}/version.pid)" 2>/dev/null; then
    echo "Version server already running (PID $(cat ${PID_DIR}/version.pid))"
else
    ./bin/mosaic-version-server > "${LOG_DIR}/version.log" 2>&1 &
    echo $! > "${PID_DIR}/version.pid"
    echo "✓ Version server started (PID $!)"
fi

echo ""
echo "Servers running:"
echo "  STUN:      ${PUBLIC_IP}:3478 (UDP)"
echo "  TURN:      ${PUBLIC_IP}:3479 (UDP relay)"
echo "  TCP relay: ${PUBLIC_IP}:443  (TLS — fallback for UDP-blocked networks)"
echo "  Version:   ${PUBLIC_IP}:8080"
echo ""
echo "Logs: ${LOG_DIR}/"
echo "  tail -f ${LOG_DIR}/stun.log"
echo "  tail -f ${LOG_DIR}/turn.log"
echo "  tail -f ${LOG_DIR}/version.log"
