#!/bin/bash
# Deploy / update the projector web remote. Idempotent: safe to re-run after edits.
# Usage: ./deploy.sh
set -euo pipefail

TV=192.168.220.53:5555          # projector adb endpoint
PORT=8790                        # web remote port (must match device/boot.sh)
REMOTE=/data/local/tmp/tvremote  # on-device install dir
HERE="$(cd "$(dirname "$0")" && pwd)"

echo ">> connect + root + remount"
adb connect "$TV" >/dev/null
adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
adb -s "$TV" remount >/dev/null

echo ">> push web app to $REMOTE"
adb -s "$TV" shell "mkdir -p $REMOTE/cgi-bin"
adb -s "$TV" push "$HERE/device/index.html" "$REMOTE/index.html"
adb -s "$TV" push "$HERE/device/cgi-bin/."  "$REMOTE/cgi-bin/"    # all CGIs
adb -s "$TV" push "$HERE/device/boot.sh"    "$REMOTE/boot.sh"
adb -s "$TV" shell "chmod 755 $REMOTE/cgi-bin/* $REMOTE/boot.sh; chmod 644 $REMOTE/index.html"

echo ">> install boot service /vendor/etc/init/tvremote.rc"
adb -s "$TV" push "$HERE/device/tvremote.rc" /vendor/etc/init/tvremote.rc
adb -s "$TV" shell "chmod 644 /vendor/etc/init/tvremote.rc; chown root:root /vendor/etc/init/tvremote.rc; \
  chcon u:object_r:vendor_configs_file:s0 /vendor/etc/init/tvremote.rc"

echo ">> (re)start service now, no reboot needed"
# init already knows 'tvremote' after first boot; ctl.restart re-execs it. On the very
# first deploy (before a reboot) init hasn't parsed the .rc yet, so fall back to manual.
adb -s "$TV" shell "setprop ctl.restart tvremote 2>/dev/null; sleep 1; \
  if [ \"\$(getprop init.svc.tvremote)\" != running ]; then \
    pkill -f 'busybox httpd' 2>/dev/null; \
    nohup setsid /vendor/bin/busybox httpd -f -p $PORT -h $REMOTE </dev/null >$REMOTE/httpd.log 2>&1 & \
  fi"

echo ">> verify"
sleep 2
code=$(curl -s -m6 -o /dev/null -w '%{http_code}' "http://${TV%:*}:$PORT/")
echo "   http://${TV%:*}:$PORT/  ->  HTTP $code"
[ "$code" = 200 ] && echo "OK. iPhone: open that URL in Safari -> Add to Home Screen." \
                   || { echo "FAILED (see: ./logs.sh)"; exit 1; }
