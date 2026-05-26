#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${OI_INSTALL_DIR:-$HOME/.local/bin}"
TARGET="$INSTALL_DIR/oi"

if [[ -e "$TARGET" ]]; then
  rm -f "$TARGET"
  echo "removed: $TARGET"
else
  echo "not installed: $TARGET"
fi
