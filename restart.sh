#!/bin/bash
# Restart the running remote without redeploying (e.g. after it wedged). No reboot.
set -euo pipefail
TV=192.168.220.53:5555; PORT=8790; REMOTE=/data/local/tmp/tvremote
adb connect "$TV" >/dev/null; adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
adb -s "$TV" shell "setprop ctl.restart tvremote 2>/dev/null; sleep 1; \
  if [ \"\$(getprop init.svc.tvremote)\" != running ]; then \
    pkill -f 'busybox httpd' 2>/dev/null; \
    nohup setsid /vendor/bin/busybox httpd -f -p $PORT -h $REMOTE </dev/null >$REMOTE/httpd.log 2>&1 & fi"
sleep 2
echo "init.svc.tvremote = $(adb -s "$TV" shell getprop init.svc.tvremote | tr -d '\r')"
echo "HTTP $(curl -s -m6 -o /dev/null -w '%{http_code}' http://${TV%:*}:$PORT/)"
