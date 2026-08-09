#!/system/bin/sh
# Started by init (service tvremote) on boot_completed, or launched directly by
# deploy.sh on a box where init cannot be used. Foreground on purpose: init
# supervises this pid and restarts it if it exits.
#
# There is no HTTP backend any more. The server is one static binary that
# embeds the UI and injects input itself, so this script only has to wait for
# /data and exec it. That is what makes the same deploy work on a box with no
# busybox.
# Derive the install dir from where THIS script lives, rather than hardcoding a
# path. Hardcoding meant a deploy to a non-default REMOTE_DIR would read the
# default location's config and then serve that installation's token and certs
# -- one install silently operating on another's files.
B="${0%/*}"
[ "$B" = "$0" ] && B=/data/local/tmp/tvremote   # invoked with no path component

# Written by deploy.sh. The defaults only apply if it is missing, which happens
# if this runs before a deploy has finished.
HTTPS_PORT=8443
REMOTE_DIR="$B"
MDNS_HOST=androidtvremote
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
