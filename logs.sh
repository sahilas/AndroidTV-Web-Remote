#!/bin/bash
# Show service state + httpd log + recent init logcat for the remote.
set -euo pipefail
TV=192.168.220.53:5555; REMOTE=/data/local/tmp/tvremote
adb connect "$TV" >/dev/null; adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
echo "== init.svc.tvremote =="; adb -s "$TV" shell getprop init.svc.tvremote
echo "== listening on 8790? =="; adb -s "$TV" shell "toybox netstat -ltn 2>/dev/null | grep 8790 || echo none"
echo "== httpd.log =="; adb -s "$TV" shell "cat $REMOTE/httpd.log 2>/dev/null || echo '(none)'"
echo "== init logcat (tvremote) =="; adb -s "$TV" shell "logcat -d -s init 2>/dev/null | grep -i tvremote | tail -20 || true"
