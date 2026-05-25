#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DROPLET=false
DEPLOY=false
for arg in "$@"; do
    case $arg in
        -d)  DROPLET=true ;;
        -dd) DEPLOY=true ;;
        *)   echo "Usage: $0 [-d | -dd]"; exit 1 ;;
    esac
done

if $DEPLOY; then
    "$SCRIPT_DIR/deploy.sh"
fi

"$SCRIPT_DIR/install.sh" -w

if $DEPLOY; then
    echo -n
    echo -n "Did you deploy on droplet as well? (yes to procede): "
    read confirm
        if [ "$confirm" != "yes" ]; then
            echo "Aborted."
            exit 0
        fi
fi

if $DROPLET; then
    "$SCRIPT_DIR/stop.sh"
    "$SCRIPT_DIR/start.sh"
fi

"$SCRIPT_DIR/install.sh"

ENV_FILE="$SCRIPT_DIR/../.env"
LOGIN_KEY=""
if [ -f "$ENV_FILE" ]; then
    LOGIN_KEY=$(grep -E '^Login_Key' "$ENV_FILE")
fi

if [ -n "$LOGIN_KEY" ]; then
    mos login "$LOGIN_KEY"
else
    mos login test
fi

mos join network
