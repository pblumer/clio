#!/usr/bin/env bash
# M04 - Optimistic Concurrency am Bankkonto:
#   Invariante 1: ein Konto wird nur einmal eroeffnet.
#   Invariante 2: kein verlorenes Update (optimistisches Sperren).
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

# Eindeutiges Konto pro Lauf -> Demo ist wiederholbar.
ACC="/accounts/demo-$(date +%s)"
echo "Konto: ${ACC}"

# ---------------------------------------------------------------------------
section "Invariante 1 - Konto eroeffnen (isSubjectPristine) -> 200/201"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"bank\",\"subject\":\"${ACC}\",\"type\":\"opened\",\"data\":{\"owner\":\"Ada\"}}],
       \"preconditions\":[{\"type\":\"isSubjectPristine\",\"payload\":{\"subject\":\"${ACC}\"}}]}"

section "Konto ein zweites Mal eroeffnen -> erwartet HTTP 409"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"bank\",\"subject\":\"${ACC}\",\"type\":\"opened\",\"data\":{\"owner\":\"Mallory\"}}],
       \"preconditions\":[{\"type\":\"isSubjectPristine\",\"payload\":{\"subject\":\"${ACC}\"}}]}"

# Eine Einzahlung, damit der Stream einen 'Stand' hat.
section "Einzahlung 100 (ohne Precondition) -> 200/201"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"bank\",\"subject\":\"${ACC}\",\"type\":\"deposited\",\"data\":{\"amount\":100}}]}"

# ---------------------------------------------------------------------------
# Invariante 2 - optimistisches Sperren.
# Schritt 1: Stream lesen und die ID des LETZTEN Events ermitteln.
section "Stream lesen, letzte Event-ID ermitteln"
LAST_ID="$(curl -sS -X POST "${CLIO_BASE}/api/v1/read-events" "${AUTH[@]}" \
  -d "{\"subject\":\"${ACC}\"}" \
  | grep -o '"id":"[0-9]*"' | tail -n1 | grep -o '[0-9]*')"
echo "letzte Event-ID: ${LAST_ID}"

section "Abhebung NUR, wenn letztes Event noch ID ${LAST_ID} ist (isSubjectOnEventId) -> 200/201"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"bank\",\"subject\":\"${ACC}\",\"type\":\"withdrawn\",\"data\":{\"amount\":30}}],
       \"preconditions\":[{\"type\":\"isSubjectOnEventId\",\"payload\":{\"subject\":\"${ACC}\",\"eventId\":\"${LAST_ID}\"}}]}"

section "Zweite Abhebung mit der VERALTETEN ID ${LAST_ID} -> erwartet HTTP 409"
echo "(In der Zwischenzeit hat sich der Stream durch die erste Abhebung veraendert.)"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"bank\",\"subject\":\"${ACC}\",\"type\":\"withdrawn\",\"data\":{\"amount\":40}}],
       \"preconditions\":[{\"type\":\"isSubjectOnEventId\",\"payload\":{\"subject\":\"${ACC}\",\"eventId\":\"${LAST_ID}\"}}]}"

echo
echo "Lehre: bei 409 liest die App neu, prueft die Regel erneut und versucht es"
echo "mit aktualisierter Precondition wieder (Retry-Schleife) - kein verlorenes Update."
