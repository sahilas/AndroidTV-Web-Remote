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
PORT="$BACKEND_PORT"
REMOTE="$REMOTE_DIR"

if [ -z "$IP" ] || [ "$IP" = "$TV" ]; then
  echo "!! TV_ADDR must be host:port (got '$TV') — see config.env" >&2
  exit 1
fi

# adb connect + become root where possible. Returns 0 if the shell is root, 1 if
# it is the unprivileged `shell` user. Callers decide whether that is fatal:
# pushing to /data/local/tmp and injecting input both work as `shell`, but
# writing /vendor (the boot service) does not.
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
