#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
	echo "uninstall-local.sh is macOS-only." >&2
	exit 1
fi

REMOVE_KEY=false
if [[ "${1:-}" == "--remove-key" ]]; then
	REMOVE_KEY=true
fi

LABEL="io.mosaic.watcher"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"
APP="$HOME/Applications/Mosaic.app"
BIN="$HOME/Mosaic/.bin/mosaic-client"
KEY_FILE="$HOME/Mosaic/.mosaic-key"

echo "==> Stopping watcher LaunchAgent"
launchctl bootout "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
rm -f "$PLIST"

echo "==> Removing Mosaic.app and client binary"
rm -rf "$APP"
rm -f "$BIN"

if [[ "$REMOVE_KEY" == true ]]; then
	echo "==> Removing key file"
	rm -f "$KEY_FILE"
else
	echo "==> Keeping key file at ${KEY_FILE}"
fi

echo "Done."
