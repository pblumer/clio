#!/usr/bin/env bash
# M05 - Event-Schemas: Schema registrieren, gueltiges und ungueltiges Event.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Schema fuer Typ 'acquired' registrieren (title Pflicht)"
# Hinweis: gelingt nur, wenn bereits gespeicherte 'acquired'-Events konform sind.
# Schemas sind unveraenderlich (erneute Registrierung -> 409).
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/register-event-schema" "${AUTH[@]}" \
  -d '{"type":"acquired","schema":{"type":"object","required":["title"],
       "properties":{"title":{"type":"string"},"author":{"type":"string"}}}}'

section "Gueltiges Event (hat title) -> erwartet HTTP 200/201"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/77","type":"acquired","data":{"title":"Snow Crash"}}]}'

section "Ungueltiges Event (title fehlt) -> erwartet HTTP 400"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[{"source":"library","subject":"/books/78","type":"acquired","data":{"author":"Stephenson"}}]}'

section "read-event-types zeigt hasSchema"
curl -sS "${AUTH[@]}" "${CLIO_BASE}/api/v1/read-event-types"

echo
echo "Schema lesen: curl ... '${CLIO_BASE}/api/v1/read-event-schema?type=acquired'"
