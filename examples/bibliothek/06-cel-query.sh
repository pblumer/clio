#!/usr/bin/env bash
# M06 - Abfragen mit CEL (run-query): Filter ueber event.type und event.data.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Alle Ausleihen (event.type == 'borrowed') ueber alle Buecher"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true,
       "where":"event.type == '\''borrowed'\''"}'

section "Ausleihen eines bestimmten Mitglieds (mit has(...)-Guard)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true,
       "where":"event.type == '\''borrowed'\'' && has(event.data.member) && event.data.member == '\''m-7'\''"}'

section "Mit Limit (hoechstens 1 Treffer)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true,"limit":1,
       "where":"event.type == '\''acquired'\''"}'

echo
echo "has(event.data.x) schuetzt vor fehlenden Feldern; ein Auswertungsfehler"
echo "eines einzelnen Events gilt als 'kein Treffer' (Query bricht nicht ab)."
