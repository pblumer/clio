#!/usr/bin/env bash
# M02 - Lesen & Filtern: Einzel-Stream, recursive, Bounds, Typ-Filter, GET-Route.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Ein Stream lesen (/books/42)"
curl -sS -X POST "${CLIO_BASE}/api/v1/read-events" "${AUTH[@]}" \
  -d '{"subject":"/books/42"}'

section "Rekursiv: alle Buecher (/books, recursive)"
curl -sS -X POST "${CLIO_BASE}/api/v1/read-events" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true}'

section "ID-Bereich (lowerBound/upperBound, beide inklusive)"
curl -sS -X POST "${CLIO_BASE}/api/v1/read-events" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true,"lowerBound":"1","upperBound":"3"}'

section "Typ-Filter: nur borrowed/returned ueber alle Buecher"
curl -sS -X POST "${CLIO_BASE}/api/v1/read-events" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true,"types":["borrowed","returned"]}'

section "GET-Komfortroute (recursive default true): alles unter /books"
curl -sS "${AUTH[@]}" "${CLIO_BASE}/api/v1/events/books"

section "GET-Route mit Optionen: nur borrowed ab ID 1"
curl -sS "${AUTH[@]}" "${CLIO_BASE}/api/v1/events/books?type=borrowed&lowerBound=1"

echo
echo "Fertig. Weiter mit 03-observe.sh"
