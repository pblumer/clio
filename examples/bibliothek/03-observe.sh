#!/usr/bin/env bash
# M03 - Live beobachten: startet einen Observer im Hintergrund, schreibt dann
# Events und zeigt, wie sie live eintreffen.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Starte Observer auf /books (recursive, live)"
# -N = ungepuffert; im Hintergrund, Ausgabe nach stdout.
curl -sN -X POST "${CLIO_BASE}/api/v1/observe-events" "${AUTH[@]}" \
  -d '{"subject":"/books","recursive":true}' &
OBSERVER_PID=$!
# Sicherstellen, dass der Observer beim Beenden aufgeraeumt wird.
trap 'kill "${OBSERVER_PID}" 2>/dev/null || true' EXIT

# Kurz warten, bis der Observer steht und die History ausgegeben hat.
sleep 1

section "Schreibe neue Events -> sollten oben SOFORT erscheinen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/99","type":"acquired","data":{"title":"Live-Demo"}}]}' >/dev/null
sleep 1
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/99","type":"borrowed","data":{"member":"m-1"}}]}' >/dev/null
sleep 1

echo
echo "Fertig. Der Observer wird jetzt beendet."
echo "Reconnect-Idee: erneut mit \"lowerBound\":\"<letzte gesehene ID>\" verbinden."
