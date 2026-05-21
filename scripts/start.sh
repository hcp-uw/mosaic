#!/usr/bin/env bash
# scripts/start.sh — start STUN and TURN servers on the droplet.
# Run from the repo root on the droplet:
#   ./scripts/start.sh [public-ip] [-quic|-udp]
#
# If no IP is given, it is read from internal/cli/shared/paths.go (DefaultServerIP).
#
# Dev flags (optional, order-independent):
#   -quic   Force all shard transfers to use QUIC only (no UDP fallback).
#   -udp    Force all shard transfers to use UDP only (no QUIC).
#           These flags also build and start a local mosaicd with MOSAIC_TRANSPORT set.
#           Running start.sh without flags preserves the default auto behaviour.

set -e

PATHS_FILE="internal/cli/shared/paths.go"
FORCE_TRANSPORT=""
POSITIONAL_ARGS=()

# Parse flags and positional args in any order.
for arg in "$@"; do
    case "$arg" in
        -quic) FORCE_TRANSPORT="quic" ;;
        -udp)  FORCE_TRANSPORT="udp" ;;
        *)     POSITIONAL_ARGS+=("$arg") ;;
    esac
done

if [ -n "${POSITIONAL_ARGS[0]:-}" ]; then
    PUBLIC_IP="${POSITIONAL_ARGS[0]}"
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
if [ ! -f "./bin/mosaic-stun" ] || [ ! -f "./bin/mosaic-turn" ]; then
    printf "Building server binaries..."
    if go build -o bin/mosaic-stun ./cmd/mosaic-stun && go build -o bin/mosaic-turn ./cmd/mosaic-turn; then
        echo " ✓"
    else
        echo " ✗"
        echo "Build failed. Run 'go build ./...' for details."
        exit 1
    fi
fi

# STUN server
if [ -f "${PID_DIR}/stun.pid" ] && kill -0 "$(cat ${PID_DIR}/stun.pid)" 2>/dev/null; then
    echo "STUN server already running (PID $(cat ${PID_DIR}/stun.pid))"
else
    ./bin/mosaic-stun > "${LOG_DIR}/stun.log" 2>&1 &
    echo $! > "${PID_DIR}/stun.pid"
    echo "✓ STUN server started (PID $!)"
fi

# TURN server
if [ -f "${PID_DIR}/turn.pid" ] && kill -0 "$(cat ${PID_DIR}/turn.pid)" 2>/dev/null; then
    echo "TURN server already running (PID $(cat ${PID_DIR}/turn.pid))"
else
    ./bin/mosaic-turn -public-ip "$PUBLIC_IP" > "${LOG_DIR}/turn.log" 2>&1 &
    echo $! > "${PID_DIR}/turn.pid"
    echo "✓ TURN server started (PID $!)"
fi

# Dev transport override: build and start mosaicd with forced transport mode.
if [ -n "$FORCE_TRANSPORT" ]; then
    printf "Building mosaicd (transport=%s)..." "$FORCE_TRANSPORT"
    if go build -o bin/mosaicd ./cmd/mosaic-node; then
        echo " ✓"
    else
        echo " ✗"
        echo "Build failed. Run 'go build ./...' for details."
        exit 1
    fi

    MOSAIC_DAEMON_LOG="${LOG_DIR}/mosaicd.log"
    MOSAIC_DAEMON_PID="${PID_DIR}/mosaicd.pid"

    # Kill any running mosaicd (regardless of which PID file owns it) so the new
    # one can claim /tmp/mosaicd.sock cleanly.
    pkill -TERM mosaicd 2>/dev/null || true
    sleep 1
    pkill -9 mosaicd 2>/dev/null || true
    rm -f /tmp/mosaicd.pid /tmp/mosaicd.sock "$MOSAIC_DAEMON_PID"

    MOSAIC_TRANSPORT="$FORCE_TRANSPORT" ./bin/mosaicd > "$MOSAIC_DAEMON_LOG" 2>&1 &
    echo $! > "$MOSAIC_DAEMON_PID"
    echo "✓ mosaicd started (PID $!, MOSAIC_TRANSPORT=$FORCE_TRANSPORT)"
    echo "  Log: tail -f ${MOSAIC_DAEMON_LOG}"
fi

echo ""
echo "Servers running:"
echo "  STUN: ${PUBLIC_IP}:3478"
echo "  TURN: ${PUBLIC_IP}:3479"
echo ""
echo "Logs: ${LOG_DIR}/"
echo "  tail -f ${LOG_DIR}/stun.log"
echo "  tail -f ${LOG_DIR}/turn.log"
