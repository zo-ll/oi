#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${OI_INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="oi"
TARGET="$INSTALL_DIR/$BINARY_NAME"
VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not installed or not in PATH" >&2
  exit 1
fi

# Some environments do not export GOPATH/GOMODCACHE. Module builds fail there
# with: "module cache not found: neither GOMODCACHE nor GOPATH is set".
# Keep caller overrides, otherwise fall back to the standard per-user paths.
export GOMODCACHE="${GOMODCACHE:-$HOME/go/pkg/mod}"
export GOCACHE="${GOCACHE:-$HOME/.cache/go-build}"

mkdir -p "$INSTALL_DIR" "$GOMODCACHE" "$GOCACHE"

tmp="$(mktemp "$INSTALL_DIR/.oi-build-XXXXXX")"
trap 'rm -f "$tmp"' EXIT

cd "$ROOT_DIR"
if ! go build -ldflags "$LDFLAGS" -o "$tmp" ./cmd/oi; then
  echo "go build failed; retrying after go clean -cache -testcache" >&2
  go clean -cache -testcache
  go build -ldflags "$LDFLAGS" -o "$tmp" ./cmd/oi
fi
mv "$tmp" "$TARGET"
chmod 0755 "$TARGET"

echo "installed: $TARGET"
echo "version: $VERSION ($COMMIT)"
echo "try: oi help"
