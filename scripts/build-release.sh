#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/dist}"
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo none)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
BINARY_NAME="oi"

platforms=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
)

mkdir -p "$OUT_DIR"
rm -f "$OUT_DIR"/*.tar.gz "$OUT_DIR"/checksums.txt

for platform in "${platforms[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"
  name="${BINARY_NAME}_${VERSION#v}_${os}_${arch}"
  staging="$OUT_DIR/$name"
  archive="$OUT_DIR/$name.tar.gz"
  rm -rf "$staging"
  mkdir -p "$staging"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build \
      -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
      -o "$staging/$BINARY_NAME" \
      ./cmd/oi
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$staging/"
  tar -C "$OUT_DIR" -czf "$archive" "$name"
  rm -rf "$staging"
  echo "built $archive"
done

(
  cd "$OUT_DIR"
  sha256sum ./*.tar.gz > checksums.txt
)

echo "checksums: $OUT_DIR/checksums.txt"
