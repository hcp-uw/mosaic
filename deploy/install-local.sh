#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
	echo "install-local.sh is macOS-only." >&2
	exit 1
fi

if [[ $# -lt 1 ]]; then
	echo "Usage: deploy/install-local.sh <key> [relay host:port]" >&2
	exit 1
fi

KEY="$1"
RELAY="${2:-45.32.226.71:9000}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$HOME/Mosaic/.bin/mosaic-client"
APP="$HOME/Applications/Mosaic.app"
PLIST="$HOME/Library/LaunchAgents/io.mosaic.watcher.plist"
LABEL="io.mosaic.watcher"
LOG_OUT="$HOME/Library/Logs/mosaic-watcher.out.log"
LOG_ERR="$HOME/Library/Logs/mosaic-watcher.err.log"

echo "==> Building client -> $BIN"
mkdir -p "$(dirname "$BIN")"
(cd "$REPO_ROOT" && go build -o "$BIN" ./client)

echo "==> Setting key in ~/Mosaic/.mosaic-key"
"$BIN" -set-key "$KEY"

echo "==> Installing Mosaic.app"
RELAY="$RELAY" BIN="$BIN" APP="$APP" "$REPO_ROOT/deploy/install-mosaic-app.sh" "$RELAY"

echo "==> Writing LaunchAgent plist"
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs" "$HOME/Mosaic/.shards"
cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BIN}</string>
    <string>-relay</string>
    <string>${RELAY}</string>
    <string>-home</string>
    <string>${HOME}/Mosaic</string>
    <string>-node</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_OUT}</string>
  <key>StandardErrorPath</key>
  <string>${LOG_ERR}</string>
</dict>
</plist>
PLIST

echo "==> (Re)loading LaunchAgent"
launchctl bootout "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
launchctl kickstart -k "gui/$(id -u)/${LABEL}"

echo
echo "Installed."
echo "Watcher label: ${LABEL}"
echo "Relay: ${RELAY}"
echo "App: ${APP}"
echo "Logs:"
echo "  ${LOG_OUT}"
echo "  ${LOG_ERR}"
