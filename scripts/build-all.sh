#!/usr/bin/env bash
# Cross-compile pennywise for every release target.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

bash scripts/tailwind.sh
mkdir -p dist

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

for t in "${targets[@]}"; do
  read -r os arch <<<"$t"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  out="dist/pennywise-${VERSION}-${os}-${arch}${ext}"
  echo "building $out"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/Arthurobo/pennywise/internal/cli.version=${VERSION} \
      -X github.com/Arthurobo/pennywise/internal/cli.commit=${COMMIT} \
      -X github.com/Arthurobo/pennywise/internal/cli.buildDate=${DATE}" \
    -o "$out" ./cmd/pennywise
done

echo
echo "binaries:"
ls -lh dist/
