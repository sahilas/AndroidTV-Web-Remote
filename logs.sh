#!/bin/bash
# Show service state + httpd log + recent init logcat for the remote.
set -euo pipefail
TV=192.168.220.53:5555; REMOTE=/data/local/tmp/tvremote
adb connect "$TV" >/dev/null; adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
echo "== init.svc.tvremote =="; adb -s "$TV" shell getprop init.svc.tvremote
echo "== listening 8790(http)/8443(https)? =="; adb -s "$TV" shell "toybox netstat -ltn 2>/dev/null | grep -E '8790|8443' || echo none"
# -o ARGS is load-bearing: busybox httpd's process NAME is "busybox", so a bare
# `ps -A` prints no line matching /httpd/ and this reads as "backend is dead"
# while it is in fact serving. Match the full argv instead.
echo "== procs =="; adb -s "$TV" shell "ps -A -o PID,ARGS 2>/dev/null | grep -iE 'tlsproxy|httpd' | grep -v grep || echo none"
echo "== token present? (a missing token makes the proxy answer 503) =="
adb -s "$TV" shell "[ -s $REMOTE/token ] && echo 'yes ('\$(wc -c <$REMOTE/token)' bytes)' || echo 'NO — run ./deploy.sh'"
echo "== boot.log / httpd.log / proxy.log =="; adb -s "$TV" shell "cat $REMOTE/boot.log $REMOTE/httpd.log $REMOTE/proxy.log 2>/dev/null || echo '(none)'"
echo "== init logcat (tvremote) =="; adb -s "$TV" shell "logcat -d -s init 2>/dev/null | grep -i tvremote | tail -20 || true"
