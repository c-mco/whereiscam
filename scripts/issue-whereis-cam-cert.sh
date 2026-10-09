#!/bin/bash
set -euo pipefail

# Script to issue and install Let's Encrypt certificate for whereis.cam
# Matching the itspartyti.me setup using acme.sh

ACME_BIN="/opt/homebrew/bin/acme.sh"
ACME_HOME="/Users/cam/.acme.sh"
CERTS_DIR="/Users/cam/stewart/certs"
WEBROOT_DIR="/Users/cam/stewart/config/nginx/html"

MODE="${1:-dns_aws}"  # "dns_aws" (default, matching itspartyti.me) or "webroot"

echo "=== Let's Encrypt Certificate Setup for whereis.cam ==="
echo "Mode: $MODE"

# Pre-flight DNS check
RESOLVED_IP=$(dig @1.1.1.1 +short whereis.cam | tail -n1 || true)
echo "whereis.cam currently resolves to: ${RESOLVED_IP:-<unresolved>}"

if [ "$MODE" = "dns_aws" ]; then
    echo "Issuing certificate via Route 53 (dns_aws)..."
    "$ACME_BIN" --home "$ACME_HOME" --issue \
        --dns dns_aws \
        -d whereis.cam \
        -d www.whereis.cam \
        --keylength ec-256
elif [ "$MODE" = "webroot" ]; then
    echo "Issuing certificate via Nginx webroot HTTP-01 challenge..."
    "$ACME_BIN" --home "$ACME_HOME" --issue \
        -w "$WEBROOT_DIR" \
        -d whereis.cam \
        -d www.whereis.cam \
        --keylength ec-256
else
    echo "Unknown mode: $MODE. Use 'dns_aws' or 'webroot'."
    exit 1
fi

echo "Installing certificate into $CERTS_DIR..."
"$ACME_BIN" --home "$ACME_HOME" --install-cert \
    -d whereis.cam \
    --ecc \
    --key-file "$CERTS_DIR/whereis.cam.key" \
    --cert-file "$CERTS_DIR/whereis.cam.cer" \
    --fullchain-file "$CERTS_DIR/whereis.cam.fullchain.cer" \
    --reloadcmd "docker exec nginx nginx -s reload"

echo "=== SSL Certificate Successfully Installed and Registered for Auto-Renewal! ==="
