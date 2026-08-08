#!/bin/bash
# Cross-compile the TLS proxy for the projector (armv7, static, no cgo).
# deploy.sh calls this, so editing tls-proxy/main.go can never ship a stale
# binary. Run standalone to just rebuild.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/device/bin/tlsproxy"

mkdir -p "$HERE/device/bin"
cd "$HERE/tls-proxy"

# CGO_ENABLED=0 forces internal linking: needed for a static armv7 binary, and
# it also dodges a go1.21-on-modern-macOS external-linker bug (missing LC_UUID)
# that breaks host test binaries.
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags="-s -w" -o "$OUT" .

echo ">> built $OUT"
file "$OUT" 2>/dev/null || ls -la "$OUT"
