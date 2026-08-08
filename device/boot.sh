#!/system/bin/sh
# Started by init (service tvremote) on boot_completed. Foreground: init supervises.
# Waits for /data, starts the busybox httpd backend, then execs the HTTPS proxy.
B=/data/local/tmp/tvremote
i=0
while { [ ! -x "$B/cgi-bin/k" ] || [ ! -f "$B/cert.pem" ]; } && [ $i -lt 60 ]; do
  sleep 2; i=$((i+1))
done
# HTTP backend, bound to LOOPBACK ONLY. It must not be reachable from the LAN:
# the CGIs inject key events as root, so a LAN-visible httpd would let anything
# on the WiFi skip the proxy's token gate entirely. The proxy serves ca.crt in
# the clear on :8443, so cert bootstrap no longer needs this port from outside.
pkill -f 'busybox httpd' 2>/dev/null
setsid /vendor/bin/busybox httpd -p 127.0.0.1:8790 -h "$B"
# HTTPS on :8443 (init supervises this pid; if it exits, the service restarts)
exec "$B/bin/tlsproxy"
