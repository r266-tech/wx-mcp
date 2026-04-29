#!/usr/bin/env bash
# Build a self-contained distribution zip: wx-mcp + wxkey binaries + bundled
# libWCDB.dylib + README. Friend解压后 `claude mcp add wx-mcp /path/to/wx-mcp` 即可用.
# 前提: SIP 已关 + 微信 4.x 登录态 + 至少开过一个会话 (首次 key scan).
set -euo pipefail

VERSION="${1:-1.0.0}"
SRCDIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$SRCDIR"

DYLIB_SRC=/Applications/WeFlow.app/Contents/Resources/resources/wcdb/macos/universal/libWCDB.dylib
if [[ ! -f "$DYLIB_SRC" ]]; then
  echo "ERROR: $DYLIB_SRC not found — WeFlow not installed on this build host" >&2
  exit 1
fi

WXKEY_SRC="$HOME/cc-workspace/mcp-servers/wxkey"
if [[ ! -d "$WXKEY_SRC" ]]; then
  echo "ERROR: $WXKEY_SRC not found — wxkey CLI source missing on build host" >&2
  exit 1
fi

DIST="$SRCDIR/dist/wx-mcp-v${VERSION}-darwin-arm64"
rm -rf "$DIST" && mkdir -p "$DIST"

echo "→ building wx-mcp binary..."
go build -o "$DIST/wx-mcp" ./cmd/wx-mcp
chmod +x "$DIST/wx-mcp"

echo "→ building wxkey binary..."
( cd "$WXKEY_SRC" && go build -o "$DIST/wxkey" ./cmd/wxkey )
chmod +x "$DIST/wxkey"

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
