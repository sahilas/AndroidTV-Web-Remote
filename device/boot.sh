#!/system/bin/sh
# Started by init (service tvremote) on boot_completed. Foreground: init supervises.
# Wait for /data to be mounted/decrypted, then serve the web remote as root.
i=0
while [ ! -x /data/local/tmp/tvremote/cgi-bin/k ] && [ $i -lt 60 ]; do
  sleep 2; i=$((i+1))
done
exec /vendor/bin/busybox httpd -f -p 8790 -h /data/local/tmp/tvremote
