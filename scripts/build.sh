#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-0.1.0}"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}"
OUTDIR="dist"

mkdir -p "$OUTDIR"

echo "=== Duman v${VERSION} (${COMMIT}) ==="
echo ""

platforms=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "darwin/amd64"
  "darwin/arm64"
)

for platform in "${platforms[@]}"; do
  IFS='/' read -r os arch <<< "$platform"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"

  for bin in duman-relay duman-client; do
    out="${OUTDIR}/${bin}-${os}-${arch}${ext}"
    echo "  Building ${out}..."
    GOOS=$os GOARCH=$arch go build -ldflags "$LDFLAGS" -o "$out" "./cmd/${bin}"
  done
done

echo ""
echo "=== Build complete ==="
ls -lh "$OUTDIR"/duman-*
