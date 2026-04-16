#!/usr/bin/env bash
# Build a self-contained distribution zip: wx-mcp binary + bundled libWCDB.dylib
# + README. Friend解压后 `claude mcp add wx-mcp /path/to/wx-mcp` 即可用
# (首次运行仍需 WeFlow 开着抓 key; key 缓存后 WeFlow 可关).
set -euo pipefail

VERSION="${1:-1.0.0}"
cd "$(dirname "$0")/.."

DYLIB_SRC=/Applications/WeFlow.app/Contents/Resources/resources/wcdb/macos/universal/libWCDB.dylib
if [[ ! -f "$DYLIB_SRC" ]]; then
  echo "ERROR: $DYLIB_SRC not found — WeFlow not installed on this build host" >&2
  exit 1
fi

DIST=dist/wx-mcp-v${VERSION}-darwin-arm64
rm -rf "$DIST" && mkdir -p "$DIST"

echo "→ building binary..."
go build -o "$DIST/wx-mcp" ./cmd/wx-mcp
chmod +x "$DIST/wx-mcp"

echo "→ bundling libWCDB.dylib ($(du -h "$DYLIB_SRC" | cut -f1))..."
cp "$DYLIB_SRC" "$DIST/libWCDB.dylib"

echo "→ copying README..."
cp README.md "$DIST/"

echo "→ zipping..."
cd dist
zip -qr "wx-mcp-v${VERSION}-darwin-arm64.zip" "wx-mcp-v${VERSION}-darwin-arm64"

echo
echo "✓ dist/wx-mcp-v${VERSION}-darwin-arm64.zip"
ls -lh "wx-mcp-v${VERSION}-darwin-arm64.zip"
