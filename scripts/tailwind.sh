#!/usr/bin/env bash
# Build the Tailwind CSS bundle. Downloads the standalone CLI on first run.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI="$ROOT/scripts/tailwindcss"
TW_VERSION="${PENNYWISE_TAILWIND_VERSION:-v3.4.17}"

if [[ ! -x "$CLI" ]]; then
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$os-$arch" in
    linux-x86_64)   asset="tailwindcss-linux-x64" ;;
    linux-aarch64|linux-arm64) asset="tailwindcss-linux-arm64" ;;
    darwin-x86_64)  asset="tailwindcss-macos-x64" ;;
    darwin-arm64)   asset="tailwindcss-macos-arm64" ;;
    *) echo "unsupported platform: $os-$arch" >&2; exit 1 ;;
  esac
  echo "downloading tailwindcss $TW_VERSION ($asset)"
  curl -fsSL -o "$CLI" "https://github.com/tailwindlabs/tailwindcss/releases/download/$TW_VERSION/$asset"
  chmod +x "$CLI"
fi

"$CLI" \
  --config  "$ROOT/scripts/tailwind.config.js" \
  --input   "$ROOT/internal/static/css/tailwind.css" \
  --output  "$ROOT/internal/static/css/output.css" \
  --minify
