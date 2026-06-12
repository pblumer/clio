#!/usr/bin/env bash
# M04 - Optimistic Concurrency (Kurzvariante in der Bibliothek-Domaene).
# Ausfuehrliches Bankkonto-Beispiel: ../bankkonto/04-preconditions.sh
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

# Eindeutiges Subject pro Lauf, damit die Demo wiederholbar ist.
SUBJ="/books/precond-$(date +%s)"

section "isSubjectPristine: erstes acquired gelingt (Subject ${SUBJ})"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"library\",\"subject\":\"${SUBJ}\",\"type\":\"acquired\",\"data\":{\"title\":\"Unikat\"}}],
       \"preconditions\":[{\"type\":\"isSubjectPristine\",\"payload\":{\"subject\":\"${SUBJ}\"}}]}"

section "Zweites acquired mit derselben Precondition -> erwartet HTTP 409"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" \
  -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d "{\"events\":[{\"source\":\"library\",\"subject\":\"${SUBJ}\",\"type\":\"acquired\",\"data\":{\"title\":\"Duplikat\"}}],
       \"preconditions\":[{\"type\":\"isSubjectPristine\",\"payload\":{\"subject\":\"${SUBJ}\"}}]}"

echo
echo "409 = Konflikt: der Stream ist nicht mehr pristine, es wurde NICHTS geschrieben."
echo "Mehr (optimistisches Sperren) in ../bankkonto/04-preconditions.sh"
