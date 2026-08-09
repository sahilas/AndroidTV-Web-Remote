#!/bin/bash
# Remove the projector web remote (boot service + files). Reverses deploy.sh.
set -euo pipefail
. "$(cd "$(dirname "$0")" && pwd)/lib.sh"   # TV, IP, PORT, HTTPS, REMOTE

adb_connect || echo "!! not root — cannot remove the /vendor boot service"
adb -s "$TV" remount >/dev/null 2>&1 || true

echo ">> stop service + kill httpd/proxy"
adb -s "$TV" shell "setprop ctl.stop tvremote 2>/dev/null; pkill -f '[b]usybox httpd' 2>/dev/null; pkill -f '[t]lsproxy' 2>/dev/null; true"
echo ">> remove boot service + app dir"
adb -s "$TV" shell "rm -f /vendor/etc/init/tvremote.rc; rm -rf $REMOTE; true"
echo "Done. Boot service gone; will not start on next reboot."
