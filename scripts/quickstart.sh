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
mos login test
mos join network
