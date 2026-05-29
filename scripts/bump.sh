#!/usr/bin/env bash
# Usage:
#   ./scripts/bump.sh           — bump patch: 1.0.0 → 1.0.1
#   ./scripts/bump.sh minor     — bump minor: 1.0.x → 1.1.0
#   ./scripts/bump.sh major     — bump major: 1.x.x → 2.0.0

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VERSION_FILE="${SCRIPT_DIR}/../internal/version/version.go"

# Read current version from version.go
current=$(grep 'Version = ' "$VERSION_FILE" | sed 's/.*Version = "\(.*\)".*/\1/')

IFS='.' read -r major minor patch <<< "$current"

case "${1:-patch}" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
    *)
        echo "Usage: $0 [patch|minor|major]"
        exit 1
        ;;
esac

new="${major}.${minor}.${patch}"
today=$(date -u +%Y-%m-%d)

# Write new version and date back into version.go using a temp file
# (portable between macOS and Linux — avoids sed -i differences)
tmp=$(mktemp)
sed "s/Version = \".*\"/Version = \"${new}\"/" "$VERSION_FILE" \
  | sed "s/Date    = \".*\"/Date    = \"${today}\"/" > "$tmp"
mv "$tmp" "$VERSION_FILE"

git add "$VERSION_FILE"
git commit -m "Bump version to v${new}"

echo ""
echo "Bumped ${current} → ${new}  (${today})"
echo "Push with: git push"
