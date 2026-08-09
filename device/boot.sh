#!/system/bin/sh
# Started by init (service tvremote) on boot_completed, or launched directly by
# deploy.sh on a box where init cannot be used. Foreground on purpose: init
# supervises this pid and restarts it if it exits.
#
# There is no HTTP backend any more. The server is one static binary that
# embeds the UI and injects input itself, so this script only has to wait for
# /data and exec it. That is what makes the same deploy work on a box with no
# busybox.
B=/data/local/tmp/tvremote

# Written by deploy.sh. The defaults only apply if it is missing, which happens
# if this runs before a deploy has finished.
HTTPS_PORT=8443
REMOTE_DIR="$B"
MDNS_HOST=projectorremote
ADV_IP=
[ -f "$B/config" ] && . "$B/config"
B="$REMOTE_DIR"

# Wait for the install to be complete, not merely present: on a cold boot /data
# mounts after init starts services, and execing a half-pushed binary fails in a
# way that looks like a crash loop.
i=0
while { [ ! -x "$B/bin/tlsproxy" ] || [ ! -f "$B/cert.pem" ]; } && [ $i -lt 60 ]; do
  sleep 2; i=$((i+1))
done

exec "$B/bin/tlsproxy" \
  -listen ":$HTTPS_PORT" \
  -dir "$B" \
  -mdns-host "$MDNS_HOST" \
  -ip "$ADV_IP"
