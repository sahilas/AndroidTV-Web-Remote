#!/bin/bash
# Restart the running remote without redeploying (e.g. after it wedged). No reboot.
set -euo pipefail
TV=192.168.220.53:5555; PORT=8790; HTTPS=8443; REMOTE=/data/local/tmp/tvremote
IP="${TV%:*}"
adb connect "$TV" >/dev/null; adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
# Fallback path keeps the LOOPBACK-ONLY bind. Binding 0.0.0.0 here would quietly
# re-expose the root CGIs to the whole WiFi on the next wedge-recovery.
# It also kills any surviving tlsproxy: a stale one still holding :8443 makes the
# new binary die on bind while the old one keeps serving — the health check below
# would pass against code you thought you had replaced.
adb -s "$TV" shell "setprop ctl.restart tvremote 2>/dev/null; sleep 1; \
  if [ \"\$(getprop init.svc.tvremote)\" != running ]; then \
    pkill -f 'busybox httpd' 2>/dev/null; pkill -f tlsproxy 2>/dev/null; \
    nohup setsid /vendor/bin/busybox httpd -f -p 127.0.0.1:$PORT -h $REMOTE </dev/null >$REMOTE/httpd.log 2>&1 & \
    nohup setsid $REMOTE/bin/tlsproxy </dev/null >$REMOTE/proxy.log 2>&1 & fi"
sleep 2
tok=$(adb -s "$TV" shell "cat $REMOTE/token 2>/dev/null" | tr -d '\r\n' || true)
code(){ local c; c=$(curl -sk -m6 -o /dev/null -w '%{http_code}' "$@" 2>/dev/null) || c=000; echo "${c:-000}"; }
echo "init.svc.tvremote = $(adb -s "$TV" shell getprop init.svc.tvremote | tr -d '\r')"
echo "HTTPS (with token) = $(code -H "Cookie: tvr=$tok" "https://$IP:$HTTPS/")   (want 200)"
echo "backend on LAN     = $(code "http://$IP:$PORT/")   (want 000 = closed)"
