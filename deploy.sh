#!/bin/bash
# Deploy / update the projector web remote. Idempotent: safe to re-run after edits.
# Usage: ./deploy.sh
set -euo pipefail

TV=192.168.220.53:5555          # projector adb endpoint
PORT=8790                        # http backend port (must match device/boot.sh)
HTTPS=8443                       # https port (tls proxy; must match tls-proxy/main.go)
REMOTE=/data/local/tmp/tvremote  # on-device install dir
HERE="$(cd "$(dirname "$0")" && pwd)"

if [ ! -f "$HERE/device/cert.pem" ]; then
  echo "!! no cert — run ./gen-cert.sh first"; exit 1
fi

echo ">> connect + root + remount"
adb connect "$TV" >/dev/null
adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
adb -s "$TV" remount >/dev/null

echo ">> push web app to $REMOTE"
adb -s "$TV" shell "mkdir -p $REMOTE/cgi-bin $REMOTE/bin"
adb -s "$TV" push "$HERE/device/index.html" "$REMOTE/index.html"
adb -s "$TV" push "$HERE/device/cgi-bin/."  "$REMOTE/cgi-bin/"    # all CGIs
adb -s "$TV" push "$HERE/device/boot.sh"    "$REMOTE/boot.sh"
adb -s "$TV" push "$HERE/device/bin/tlsproxy" "$REMOTE/bin/tlsproxy"
adb -s "$TV" push "$HERE/device/cert.pem"   "$REMOTE/cert.pem"
adb -s "$TV" push "$HERE/device/key.pem"    "$REMOTE/key.pem"
adb -s "$TV" push "$HERE/device/cert.crt"   "$REMOTE/cert.crt"   # for iPhone download
adb -s "$TV" shell "chmod 755 $REMOTE/cgi-bin/* $REMOTE/boot.sh $REMOTE/bin/tlsproxy; \
  chmod 644 $REMOTE/index.html $REMOTE/cert.pem $REMOTE/cert.crt; chmod 600 $REMOTE/key.pem"

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
IP="${TV%:*}"
h=$(curl -s -m6 -o /dev/null -w '%{http_code}' "http://$IP:$PORT/")
s=$(curl -sk -m6 -o /dev/null -w '%{http_code}' "https://$IP:$HTTPS/")
echo "   http://$IP:$PORT/    -> HTTP $h"
echo "   https://$IP:$HTTPS/  -> HTTP $s"
if [ "$h" = 200 ] && [ "$s" = 200 ]; then
  echo "OK."
  echo "iPhone (one-time): open  http://$IP:$PORT/cert.crt  in Safari -> install profile,"
  echo "  then Settings > General > About > Certificate Trust Settings -> enable it."
  echo "Then open  https://$IP:$HTTPS/  in Safari -> Add to Home Screen (fullscreen)."
else
  echo "FAILED (see: ./logs.sh)"; exit 1
fi
