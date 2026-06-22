#!/usr/bin/env bash
set -euo pipefail

REPO="zo-ll/oi"
INSTALL_DIR="${OI_INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="oi"
TARGET="$INSTALL_DIR/$BINARY_NAME"
VERSION="${OI_VERSION:-latest}"

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

die() {
  echo "error: $*" >&2
  exit 1
}

pick_downloader() {
  if need_cmd curl; then
    echo curl
    return
  fi
  if need_cmd wget; then
    echo wget
    return
  fi
  die "need curl or wget"
}

download() {
  downloader="$1"
  url="$2"
  dest="$3"
  if [ "$downloader" = curl ]; then
    curl -fsSL "$url" -o "$dest"
    return
  fi
  wget -qO "$dest" "$url"
}

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux|darwin) echo "$os" ;;
    *) die "unsupported OS: $os" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch: $arch" ;;
  esac
}

verify_checksum() {
  archive="$1"
  checksums="$2"
  expected_line="$3"
  if need_cmd sha256sum; then
    (cd "$(dirname "$archive")" && sha256sum -c <(printf '%s\n' "$expected_line"))
    return
  fi
  if need_cmd shasum; then
    expected_sum="${expected_line%% *}"
    actual_sum="$(shasum -a 256 "$archive" | awk '{print $1}')"
    [ "$expected_sum" = "$actual_sum" ] || die "checksum mismatch"
    return
  fi
  echo "warning: sha256sum/shasum missing; skipping checksum verification" >&2
}

install_from_release() {
  downloader="$1"
  os="$2"
  arch="$3"
  version_label="$4"
  tmpdir="$5"
  asset="${BINARY_NAME}_${version_label#v}_${os}_${arch}.tar.gz"
  if [ "$version_label" = latest ]; then
    archive_url="https://github.com/$REPO/releases/latest/download/$asset"
    checksums_url="https://github.com/$REPO/releases/latest/download/checksums.txt"
  else
    archive_url="https://github.com/$REPO/releases/download/$version_label/$asset"
    checksums_url="https://github.com/$REPO/releases/download/$version_label/checksums.txt"
  fi
  archive="$tmpdir/$asset"
  checksums="$tmpdir/checksums.txt"
  download "$downloader" "$archive_url" "$archive"
  download "$downloader" "$checksums_url" "$checksums"
  line="$(grep "$asset$" "$checksums" || true)"
  [ -n "$line" ] || die "checksum entry not found for $asset"
  verify_checksum "$archive" "$checksums" "$line"
  tar -xzf "$archive" -C "$tmpdir"
  extracted="$tmpdir/${asset%.tar.gz}/$BINARY_NAME"
  [ -f "$extracted" ] || die "binary missing from archive"
  mkdir -p "$INSTALL_DIR"
  mv "$extracted" "$TARGET"
  chmod 0755 "$TARGET"
}

install_from_source() {
  root_dir="$1"
  if ! need_cmd go; then
    die "go is not installed or not in PATH"
  fi
  export GOMODCACHE="${GOMODCACHE:-$HOME/go/pkg/mod}"
  export GOCACHE="${GOCACHE:-$HOME/.cache/go-build}"
  mkdir -p "$INSTALL_DIR" "$GOMODCACHE" "$GOCACHE"
  version_build="$(git -C "$root_dir" describe --tags --always --dirty 2>/dev/null || echo dev)"
  commit_build="$(git -C "$root_dir" rev-parse --short HEAD 2>/dev/null || echo none)"
  date_build="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-X main.version=$version_build -X main.commit=$commit_build -X main.date=$date_build"
  tmpbin="$(mktemp "$INSTALL_DIR/.oi-build-XXXXXX")"
  trap 'rm -f "$tmpbin"' EXIT
  cd "$root_dir"
  if ! go build -ldflags "$ldflags" -o "$tmpbin" ./cmd/oi; then
    echo "go build failed; retrying after go clean -cache -testcache" >&2
    go clean -cache -testcache
    go build -ldflags "$ldflags" -o "$tmpbin" ./cmd/oi
  fi
  mv "$tmpbin" "$TARGET"
  chmod 0755 "$TARGET"
  echo "installed: $TARGET"
  echo "version: $version_build ($commit_build)"
  echo "try: oi help"
}

main() {
  if [ "${OI_INSTALL_FROM_SOURCE:-}" = 1 ]; then
    root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    install_from_source "$root_dir"
    exit 0
  fi

  downloader="$(pick_downloader)"
  os="$(detect_os)"
  arch="$(detect_arch)"
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT
  install_from_release "$downloader" "$os" "$arch" "$VERSION" "$tmpdir"
  echo "installed: $TARGET"
  "$TARGET" version || true
  echo "try: oi help"
}

main "$@"
