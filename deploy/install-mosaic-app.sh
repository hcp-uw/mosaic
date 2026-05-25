#!/usr/bin/env bash
# Install the Mosaic client and a macOS app that opens .mosaic stubs on
# double-click, rehydrating them from the network.
#
# Usage:
#   deploy/install-mosaic-app.sh [relay host:port]
#
# Env overrides:
#   RELAY   relay address (default 45.32.226.71:9000, or $1)
#   BIN     client binary path (default ~/Mosaic/.bin/mosaic-client)
#   APP     app bundle path    (default ~/Applications/Mosaic.app)
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
	echo "This installer is macOS-only (it builds a .app)." >&2
	exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELAY="${RELAY:-${1:-45.32.226.71:9000}}"
BIN="${BIN:-$HOME/Mosaic/.bin/mosaic-client}"
APP="${APP:-$HOME/Applications/Mosaic.app}"

echo "==> Building client -> $BIN"
mkdir -p "$(dirname "$BIN")"
(cd "$REPO_ROOT" && go build -o "$BIN" ./client)

echo "==> Generating AppleScript handler"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cat > "$WORK/handler.applescript" <<APPLESCRIPT
on open theFiles
	repeat with f in theFiles
		set p to POSIX path of f
		do shell script "'$BIN' -relay '$RELAY' -open -rehydrate " & quoted form of p & " > /tmp/mosaic-open.log 2>&1"
	end repeat
end open

on run
	display dialog "Mosaic: double-click a .mosaic file to download and open it." buttons {"OK"} default button "OK"
end run
APPLESCRIPT

echo "==> Compiling $APP"
rm -rf "$APP"
mkdir -p "$(dirname "$APP")"
osacompile -o "$APP" "$WORK/handler.applescript"

echo "==> Registering .mosaic document type"
PLIST="$APP/Contents/Info.plist"
PB=/usr/libexec/PlistBuddy
"$PB" -c "Add :CFBundleDocumentTypes array" "$PLIST" 2>/dev/null || true
"$PB" -c "Add :CFBundleDocumentTypes:0 dict" "$PLIST"
"$PB" -c "Add :CFBundleDocumentTypes:0:CFBundleTypeName string 'Mosaic Shard Stub'" "$PLIST"
"$PB" -c "Add :CFBundleDocumentTypes:0:CFBundleTypeRole string Viewer" "$PLIST"
"$PB" -c "Add :CFBundleDocumentTypes:0:LSHandlerRank string Owner" "$PLIST"
"$PB" -c "Add :CFBundleDocumentTypes:0:CFBundleTypeExtensions array" "$PLIST"
"$PB" -c "Add :CFBundleDocumentTypes:0:CFBundleTypeExtensions:0 string mosaic" "$PLIST"

echo "==> Registering app with Launch Services"
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister
"$LSREGISTER" -f "$APP"

echo
echo "Done. Mosaic.app installed at: $APP"
echo "Relay: $RELAY"
echo
echo "Double-click any .mosaic file to download and open the original."
echo "If macOS doesn't pick Mosaic automatically the first time, right-click the"
echo "stub -> Open With -> Mosaic, and tick 'Always Open With'."
