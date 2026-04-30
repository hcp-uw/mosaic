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
    echo "Usage: ./scripts/start.sh [public-ip]"
    exit 1
fi

LOG_DIR="/var/log/mosaic"
PID_DIR="/var/run/mosaic"

mkdir -p "$LOG_DIR" "$PID_DIR"

# STUN server
if [ -f "${PID_DIR}/stun.pid" ] && kill -0 "$(cat ${PID_DIR}/stun.pid)" 2>/dev/null; then
    echo "STUN server already running (PID $(cat ${PID_DIR}/stun.pid))"
else
    ./bin/mosaic-stun >> "${LOG_DIR}/stun.log" 2>&1 &
    echo $! > "${PID_DIR}/stun.pid"
    echo "✓ STUN server started (PID $!)"
fi

# TURN server
if [ -f "${PID_DIR}/turn.pid" ] && kill -0 "$(cat ${PID_DIR}/turn.pid)" 2>/dev/null; then
    echo "TURN server already running (PID $(cat ${PID_DIR}/turn.pid))"
else
    ./bin/mosaic-turn -public-ip "$PUBLIC_IP" >> "${LOG_DIR}/turn.log" 2>&1 &
    echo $! > "${PID_DIR}/turn.pid"
    echo "✓ TURN server started (PID $!)"
fi

echo ""
echo "Servers running:"
echo "  STUN: ${PUBLIC_IP}:3478"
echo "  TURN: ${PUBLIC_IP}:3479"
echo ""
echo "Logs: ${LOG_DIR}/"
echo "  tail -f ${LOG_DIR}/stun.log"
echo "  tail -f ${LOG_DIR}/turn.log"
