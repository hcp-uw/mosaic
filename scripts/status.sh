#!/usr/bin/env bash
# scripts/status.sh — show status of all Mosaic servers on the droplet.

PID_DIR="/var/run/mosaic"
LOG_DIR="/var/log/mosaic"

check_server() {
    local name="$1"
    local pid_file="${PID_DIR}/${2}.pid"
    local port="$3"

    if [ -f "$pid_file" ]; then
        local pid
        pid=$(cat "$pid_file")
        if kill -0 "$pid" 2>/dev/null; then
            echo "  ✓ ${name} running (PID ${pid}, port ${port})"
        else
            echo "  ✗ ${name} DEAD (stale PID ${pid})"
        fi
    else
        echo "  ✗ ${name} not started"
    fi
}

echo "Mosaic server status:"
check_server "Auth server" auth 8081
check_server "STUN server" stun 3478
check_server "TURN server" turn 3479
echo ""
echo "Recent logs:"
for srv in auth stun turn; do
    log="${LOG_DIR}/${srv}.log"
    if [ -f "$log" ]; then
        echo "  --- ${srv} (last 3 lines) ---"
        tail -3 "$log" | sed 's/^/  /'
    fi
done
