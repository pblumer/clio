#!/usr/bin/env bash
# Schreibt eine kleine, fachlich verständliche Order-Historie nach clio. Erwartet
# einen Token mit write-Scope in CLIO_TOKEN (z. B. der Admin-Wert aus setup.sh).
#
#   CLIO_TOKEN=kid_xxx.secret ./seed-orders.sh   [CLIO_BASE=http://127.0.0.1:3000]
set -euo pipefail
: "${CLIO_TOKEN:?CLIO_TOKEN (write-Scope) setzen}"
: "${CLIO_BASE:=http://127.0.0.1:3000}"

write() {
  curl -fsS -X POST "$CLIO_BASE/api/v1/write-events" \
    -H "Authorization: Bearer $CLIO_TOKEN" -H 'Content-Type: application/json' \
    -d "$1" >/dev/null
}

echo "seed: order o-1 (placed -> paid -> shipped)"
write '{"events":[{"source":"shop","subject":"/orders/o-1","type":"order.placed","data":{"customer":"alice","totalCents":4999}}]}'
write '{"events":[{"source":"shop","subject":"/orders/o-1","type":"order.paid"}]}'
write '{"events":[{"source":"shop","subject":"/orders/o-1","type":"order.shipped","data":{"carrier":"DHL","trackingId":"TRK-001"}}]}'

echo "seed: order o-2 (placed -> cancelled)"
write '{"events":[{"source":"shop","subject":"/orders/o-2","type":"order.placed","data":{"customer":"bob","totalCents":1200}}]}'
write '{"events":[{"source":"shop","subject":"/orders/o-2","type":"order.cancelled","data":{"reason":"out of stock"}}]}'

echo "seed: order o-3 (placed)"
write '{"events":[{"source":"shop","subject":"/orders/o-3","type":"order.placed","data":{"customer":"carol","totalCents":7350}}]}'

echo "seed fertig."
