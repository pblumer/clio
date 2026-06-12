#!/usr/bin/env bash
# M01 - Erstes Event schreiben: einzeln und atomar mehrere.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Ein einzelnes Event schreiben (/books/42 acquired)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/42","type":"acquired","data":{"title":"Dune","author":"Herbert"}}]}'
echo

section "Mehrere Events atomar (borrowed + returned in EINER Transaktion)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
        {"source":"library","subject":"/books/42","type":"borrowed","data":{"member":"m-7"}},
        {"source":"library","subject":"/books/42","type":"returned","data":{"member":"m-7"}}
      ]}'
echo

section "Ein zweites Buch anlegen (/books/43)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/43","type":"acquired","data":{"title":"Foundation","author":"Asimov"}}]}'
echo

echo
echo "Fertig. Weiter mit 02-lesen-und-filtern.sh"
