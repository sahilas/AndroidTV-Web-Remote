#!/bin/bash
# Remove the projector web remote (boot service + files). Reverses deploy.sh.
set -euo pipefail
TV=192.168.220.53:5555
REMOTE=/data/local/tmp/tvremote

adb connect "$TV" >/dev/null
adb -s "$TV" root >/dev/null; sleep 1; adb connect "$TV" >/dev/null
adb -s "$TV" remount >/dev/null

echo ">> stop service + kill httpd/proxy"
adb -s "$TV" shell "setprop ctl.stop tvremote 2>/dev/null; pkill -f 'busybox httpd' 2>/dev/null; pkill -f tlsproxy 2>/dev/null; true"
echo ">> remove boot service + app dir"
adb -s "$TV" shell "rm -f /vendor/etc/init/tvremote.rc; rm -rf $REMOTE; true"
echo "Done. Boot service gone; will not start on next reboot."
