#!/bin/bash
# Cross-compile the TLS proxy for every Android ABI we support.
# deploy.sh calls this, so editing tls-proxy/main.go can never ship a stale
# binary. Run standalone to just rebuild.
#
# Builds all ABIs rather than only the connected device's: the output is what
# somebody else's box will need, and a build that only ever targets the machine
# in front of you is how "generic" silently stops being true. Each is ~7 MB and
# they are gitignored.
#
# Pass an ABI to build just that one: ./build.sh arm64-v8a
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/device/bin"

# Android ABI -> Go toolchain triple. Keep in sync with abi_to_binary() in lib.sh.
abis="arm64-v8a armeabi-v7a x86_64 x86"
goenv_for() {
  case "$1" in
    arm64-v8a)   echo "GOARCH=arm64" ;;
    armeabi-v7a) echo "GOARCH=arm GOARM=7" ;;
    x86_64)      echo "GOARCH=amd64" ;;
    x86)         echo "GOARCH=386" ;;
    *) echo "!! unknown ABI '$1'" >&2; return 1 ;;
  esac
}

mkdir -p "$OUT"
cd "$HERE/tls-proxy"

# CGO_ENABLED=0 forces internal linking: needed for static binaries with no
# bionic/libc dependency, which is what makes one file run on any Android build.
echo ">> test (host)"
CGO_ENABLED=0 go test ./...

want="${1:-$abis}"
for abi in $want; do
  env_vars="$(goenv_for "$abi")"
  # shellcheck disable=SC2086
  env CGO_ENABLED=0 GOOS=linux $env_vars \
    go build -trimpath -ldflags="-s -w" -o "$OUT/tlsproxy-$abi" .
  echo ">> built tlsproxy-$abi"
done

ls -la "$OUT" | tail -n +2
