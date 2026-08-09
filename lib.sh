#!/bin/bash
# Sourced by every script; not runnable on its own. Loads config.env, then lets
# config.local.env override it.
#
# BASH_SOURCE, not $0: $0 is the *calling* script, so using it here would
# resolve the config path relative to whoever sourced us and break the moment a
# script is invoked from another directory.
_LIBDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=config.env
. "$_LIBDIR/config.env"
[ -f "$_LIBDIR/config.local.env" ] && . "$_LIBDIR/config.local.env"

HERE="$_LIBDIR"
TV="$TV_ADDR"
IP="${TV%:*}"          # adb endpoint minus the port
HTTPS="$HTTPS_PORT"
# Only used to assert the pre-embed busybox backend is gone; see deploy.sh.
LEGACY_BACKEND_PORT=8790
REMOTE="$REMOTE_DIR"

if [ -z "$IP" ] || [ "$IP" = "$TV" ]; then
  echo "!! TV_ADDR must be host:port (got '$TV') — see config.env" >&2
  exit 1
fi

# adb connect + become root where possible. Returns 0 if the shell is root, 1 if
# it is the unprivileged `shell` user. Callers decide whether that is fatal:
# pushing to /data/local/tmp and injecting input both work as `shell`, but
# writing /vendor (the boot service) does not.
# Map an Android ABI to the binary build.sh produced for it. Keep in sync with
# goenv_for() in build.sh.
abi_to_binary() {
  case "$1" in
    arm64-v8a)   echo "tlsproxy-arm64-v8a" ;;
    armeabi-v7a|armeabi) echo "tlsproxy-armeabi-v7a" ;;
    x86_64)      echo "tlsproxy-x86_64" ;;
    x86)         echo "tlsproxy-x86" ;;
    *)           return 1 ;;
  esac
}

# Pick the binary for the connected device. Prefers ro.product.cpu.abi, then
# walks ro.product.cpu.abilist -- a 64-bit box that can also run 32-bit code
# lists both, and we want the primary one to win rather than whichever we
# happened to check first.
select_binary() {
  local abi abilist bin
  abi="$(adb -s "$TV" shell getprop ro.product.cpu.abi 2>/dev/null | tr -d '\r')"
  if bin="$(abi_to_binary "$abi")"; then echo "$bin"; return 0; fi

  abilist="$(adb -s "$TV" shell getprop ro.product.cpu.abilist 2>/dev/null | tr -d '\r')"
  local IFS=,
  for a in $abilist; do
    if bin="$(abi_to_binary "$a")"; then echo "$bin"; return 0; fi
  done

  echo "!! unsupported CPU ABI: '$abi' (abilist: '$abilist')" >&2
  echo "   supported: arm64-v8a, armeabi-v7a, x86_64, x86" >&2
  return 1
}

adb_connect() {
  adb connect "$TV" >/dev/null 2>&1 || true
  adb -s "$TV" root >/dev/null 2>&1 || true
  sleep 1
  adb connect "$TV" >/dev/null 2>&1 || true
  case "$(adb -s "$TV" shell id -u 2>/dev/null | tr -d '\r')" in
    0) return 0 ;;
    *) return 1 ;;
  esac
}
