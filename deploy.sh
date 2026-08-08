#!/bin/bash
# Deploy / update the projector web remote. Idempotent: safe to re-run after edits.
# Usage: ./deploy.sh [--rotate-token]
set -euo pipefail

TV=192.168.220.53:5555          # projector adb endpoint
PORT=8790                        # http backend port (must match device/boot.sh)
HTTPS=8443                       # https port (tls proxy; must match tls-proxy/main.go)
REMOTE=/data/local/tmp/tvremote  # on-device install dir
HERE="$(cd "$(dirname "$0")" && pwd)"
IP="${TV%:*}"

if [ ! -f "$HERE/device/cert.pem" ]; then
  echo "!! no cert — run ./gen-cert.sh first"; exit 1
fi

# Always rebuild the proxy. Skipping this ships a stale binary while every source
# file looks correct — the most confusing possible failure.
echo ">> build tls proxy (armv7)"
"$HERE/build.sh"

echo ">> connect + root + remount"
adb connect "$TV" >/dev/null
adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
adb -s "$TV" remount >/dev/null

# ---- auth token ------------------------------------------------------------
# Lives only on the device (0600). Reused across deploys on purpose: rotating it
# invalidates every phone's saved home-screen shortcut and cookie.
echo ">> auth token"
tok=$(adb -s "$TV" shell "cat $REMOTE/token 2>/dev/null" | tr -d '\r\n' || true)
if [ "${1:-}" = "--rotate-token" ] || [ -z "$tok" ]; then
  [ -n "$tok" ] && echo "   !! rotating — every phone must reopen the new URL"
  tok=$(openssl rand -hex 16)
  adb -s "$TV" shell "mkdir -p $REMOTE; printf '%s' '$tok' > $REMOTE/token; chmod 600 $REMOTE/token"
  echo "   new token generated"
else
  echo "   reusing existing token"
fi

echo ">> push web app to $REMOTE"
adb -s "$TV" shell "mkdir -p $REMOTE/cgi-bin $REMOTE/bin"
adb -s "$TV" push "$HERE/device/index.html" "$REMOTE/index.html"
adb -s "$TV" push "$HERE/device/cgi-bin/."  "$REMOTE/cgi-bin/"    # all CGIs
adb -s "$TV" push "$HERE/device/boot.sh"    "$REMOTE/boot.sh"
adb -s "$TV" push "$HERE/device/bin/tlsproxy" "$REMOTE/bin/tlsproxy"
adb -s "$TV" push "$HERE/device/cert.pem"   "$REMOTE/cert.pem"   # fullchain: leaf + CA
adb -s "$TV" push "$HERE/device/key.pem"    "$REMOTE/key.pem"
adb -s "$TV" push "$HERE/device/ca.crt"     "$REMOTE/ca.crt"     # CA for iPhone to trust
adb -s "$TV" shell "rm -f $REMOTE/cert.crt; chmod 755 $REMOTE/cgi-bin/* $REMOTE/boot.sh $REMOTE/bin/tlsproxy; \
  chmod 644 $REMOTE/index.html $REMOTE/cert.pem $REMOTE/ca.crt; chmod 600 $REMOTE/key.pem $REMOTE/token"

echo ">> install boot service /vendor/etc/init/tvremote.rc"
adb -s "$TV" push "$HERE/device/tvremote.rc" /vendor/etc/init/tvremote.rc
adb -s "$TV" shell "chmod 644 /vendor/etc/init/tvremote.rc; chown root:root /vendor/etc/init/tvremote.rc; \
  chcon u:object_r:vendor_configs_file:s0 /vendor/etc/init/tvremote.rc"

echo ">> (re)start service now, no reboot needed"
svc=$(adb -s "$TV" shell 'getprop init.svc.tvremote' | tr -d '\r')
if [ -n "$svc" ]; then
  # init knows the service (installed on a prior boot) -> just re-exec it
  adb -s "$TV" shell "setprop ctl.restart tvremote" >/dev/null 2>&1 || true
else
  # first deploy before any reboot: launch once, fully detached so adb returns
  adb -s "$TV" shell "( setsid /system/bin/sh $REMOTE/boot.sh >$REMOTE/boot.log 2>&1 </dev/null & ); exit" >/dev/null 2>&1 || true
fi

echo ">> verify"
sleep 3
# curl prints 000 to stdout on a connection failure but also exits nonzero, which
# set -e would treat as fatal; swallow the status and keep the code.
code(){ local c; c=$(curl -sk -m6 -o /dev/null -w '%{http_code}' "$@" 2>/dev/null) || c=000; echo "${c:-000}"; }

lan=$(code "http://$IP:$PORT/")                      # must be UNREACHABLE (000)
noauth=$(code "https://$IP:$HTTPS/")                 # must be 401
auth=$(code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/")
ca=$(code "http://$IP:$HTTPS/ca.crt")                # plaintext bootstrap, must be 200
# Proves the CGI actually execs, not just that the proxy answers. Paired up/down
# so the check leaves the projector's volume where it found it.
key=$(code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/cgi-bin/k?volup")
code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/cgi-bin/k?voldown" >/dev/null
# busybox httpd serves the whole install dir as root, so a valid cookie must NOT
# be enough to read the TLS key or the token back out of it.
leakkey=$(code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/key.pem")
leaktok=$(code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/token")

echo "   backend on LAN     http://$IP:$PORT/            -> $lan   (want 000 = closed)"
echo "   no token           https://$IP:$HTTPS/          -> $noauth   (want 401)"
echo "   with token         https://$IP:$HTTPS/          -> $auth   (want 200)"
echo "   CA over plaintext  http://$IP:$HTTPS/ca.crt     -> $ca   (want 200)"
echo "   a key press        …/cgi-bin/k?volup            -> $key   (want 200)"
echo "   TLS key w/ token   …/key.pem                    -> $leakkey   (want 404)"
echo "   token w/ token     …/token                      -> $leaktok   (want 404)"

fail=0
[ "$lan"    = 000 ] || { echo "!! backend is still reachable from the LAN — token gate is bypassable"; fail=1; }
[ "$noauth" = 401 ] || { echo "!! unauthenticated request was not rejected"; fail=1; }
[ "$auth"   = 200 ] || { echo "!! tokenized request failed"; fail=1; }
[ "$ca"     = 200 ] || { echo "!! ca.crt not served in the clear — a new phone can't bootstrap trust"; fail=1; }
[ "$key"     = 200 ] || { echo "!! key injection endpoint failed"; fail=1; }
[ "$leakkey" = 404 ] || { echo "!! the TLS PRIVATE KEY is downloadable — the route allowlist is broken"; fail=1; }
[ "$leaktok" = 404 ] || { echo "!! the auth token itself is downloadable — the route allowlist is broken"; fail=1; }
if [ "$fail" != 0 ]; then echo "FAILED (see: ./logs.sh)"; exit 1; fi

echo "OK."
echo
echo "REMOTE URL (contains the token — treat it like a password):"
echo "  https://$IP:$HTTPS/?t=$tok"
echo
echo "iPhone (one-time): DELETE any old 'Projector Remote' profile first, then:"
echo "  1) Safari -> http://$IP:$HTTPS/ca.crt  -> install the 'Projector Remote Local CA' profile"
echo "  2) Settings > General > About > Certificate Trust Settings -> turn it ON"
echo "  3) force-quit Safari, then open the tokenized URL above -> Add to Home Screen"
echo "     (add it WITH ?t=… so the shortcut re-authorizes itself if the cookie is ever evicted)"
