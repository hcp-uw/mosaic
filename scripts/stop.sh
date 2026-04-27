#!/usr/bin/env bash
# scripts/stop.sh — stop all Mosaic servers on the droplet.

PID_DIR="/var/run/mosaic"

stop_server() {
    local name="$1"
    local pid_file="${PID_DIR}/${2}.pid"

    if [ -f "$pid_file" ]; then
        local pid
        pid=$(cat "$pid_file")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid"
            echo "✓ ${name} stopped (PID ${pid})"
        else
            echo "${name} was not running (stale PID)"
        fi
        rm -f "$pid_file"
    else
        echo "${name} not running"
    fi
}

stop_server "STUN server" stun
stop_server "TURN server" turn
