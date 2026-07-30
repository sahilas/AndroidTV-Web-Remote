#!/bin/bash
# Show service state + httpd log + recent init logcat for the remote.
set -euo pipefail
TV=192.168.220.53:5555; REMOTE=/data/local/tmp/tvremote
adb connect "$TV" >/dev/null; adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
echo "== init.svc.tvremote =="; adb -s "$TV" shell getprop init.svc.tvremote
echo "== listening 8790(http)/8443(https)? =="; adb -s "$TV" shell "toybox netstat -ltn 2>/dev/null | grep -E '8790|8443' || echo none"
echo "== procs =="; adb -s "$TV" shell "ps -A 2>/dev/null | grep -iE 'tlsproxy|httpd' | grep -v grep || echo none"
echo "== boot.log / httpd.log =="; adb -s "$TV" shell "cat $REMOTE/boot.log $REMOTE/httpd.log 2>/dev/null || echo '(none)'"
echo "== init logcat (tvremote) =="; adb -s "$TV" shell "logcat -d -s init 2>/dev/null | grep -i tvremote | tail -20 || true"
