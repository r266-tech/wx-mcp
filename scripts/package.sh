#!/usr/bin/env bash
# 打分发包: 单文件 wx-mcp 二进制 + README
# 朋友解压后 claude mcp add 即可 (前提: 他已装 WeFlow).
set -euo pipefail

VERSION="${1:-0.4.0}"
cd "$(dirname "$0")/.."

DIST=dist/wx-mcp-v${VERSION}-darwin-arm64
rm -rf "$DIST" && mkdir -p "$DIST"

echo "→ building..."
go build -o "$DIST/wx-mcp" ./cmd/wx-mcp
chmod +x "$DIST/wx-mcp"

echo "→ copying README..."
cp README.md "$DIST/"

echo "→ zipping..."
cd dist
zip -qr "wx-mcp-v${VERSION}-darwin-arm64.zip" "wx-mcp-v${VERSION}-darwin-arm64"

echo
echo "✓ dist/wx-mcp-v${VERSION}-darwin-arm64.zip"
ls -lh "wx-mcp-v${VERSION}-darwin-arm64.zip"
