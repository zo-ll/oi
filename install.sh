#!/bin/bash
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "${HOME}/.local/bin"
ln -sf "${SCRIPT_DIR}/oi" "${HOME}/.local/bin/oi"
echo "Installed: ~/.local/bin/oi -> ${SCRIPT_DIR}/oi"
echo "Make sure ~/.local/bin is in your PATH."
