#!/usr/bin/env bash
# Build pennywise for the host platform.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

bash scripts/tailwind.sh

CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w \
    -X github.com/Arthurobo/pennywise/internal/cli.version=${VERSION} \
    -X github.com/Arthurobo/pennywise/internal/cli.commit=${COMMIT} \
    -X github.com/Arthurobo/pennywise/internal/cli.buildDate=${DATE}" \
  -o pennywise ./cmd/pennywise

echo "built: $(ls -lh pennywise | awk '{print $5}') $(file pennywise | cut -d, -f1-2)"
