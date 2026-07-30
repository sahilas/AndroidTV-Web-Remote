#!/bin/bash
# Generate the self-signed TLS cert for the HTTPS proxy. Run once (or to renew).
# iOS requirements baked in: SAN present, serverAuth EKU, validity < 825 days.
set -euo pipefail
IP=192.168.220.53
HERE="$(cd "$(dirname "$0")" && pwd)/device"

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$HERE/key.pem" -out "$HERE/cert.pem" -days 800 \
  -subj "/CN=Projector Remote ($IP)" \
  -addext "subjectAltName=IP:$IP" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth"
cp "$HERE/cert.pem" "$HERE/cert.crt"   # served for the iPhone to download+trust
echo "Wrote device/cert.pem, device/key.pem, device/cert.crt (SAN IP:$IP)."
echo "Now run ./deploy.sh"
