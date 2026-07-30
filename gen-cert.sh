#!/bin/bash
# Generate the TLS material for the HTTPS proxy: a local CA + a short-lived leaf.
# iOS trusts the CA once; the leaf (IP SAN, serverAuth, CA:FALSE, <398d) validates
# under it. Verified against Apple's own evaluator (security verify-cert).
# Renewing the leaf later needs NO re-trust on the phone (same CA).
set -euo pipefail
IP=192.168.220.53
D="$(cd "$(dirname "$0")" && pwd)/device"

# --- CA (this is what the iPhone downloads + trusts) ---
if [ ! -f "$D/ca.pem" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -keyout "$D/ca.key" -out "$D/ca.pem" -days 3650 \
    -subj "/CN=Projector Remote Local CA" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign"
  echo "generated new CA (device/ca.pem)"
fi

# --- leaf signed by the CA ---
openssl req -newkey rsa:2048 -nodes -keyout "$D/leaf.key" -out "$D/leaf.csr" -subj "/CN=$IP"
openssl x509 -req -in "$D/leaf.csr" -CA "$D/ca.pem" -CAkey "$D/ca.key" -CAcreateserial \
  -out "$D/leaf.pem" -days 397 \
  -extfile <(printf "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:%s\n" "$IP")

# --- assemble what the device uses ---
cat "$D/leaf.pem" "$D/ca.pem" > "$D/cert.pem"   # fullchain (leaf FIRST) for the proxy
cp "$D/leaf.key" "$D/key.pem"                    # proxy private key
cp "$D/ca.pem"   "$D/ca.crt"                      # served for the iPhone to trust
rm -f "$D/leaf.csr" "$D"/*.srl

echo "OK. Sanity:"
openssl verify -CAfile "$D/ca.pem" -purpose sslserver "$D/leaf.pem"
echo "Now: ./deploy.sh   (then re-trust the NEW CA on the phone)"
