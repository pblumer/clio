#!/usr/bin/env bash
# I06 — CEL-Queries zur Verifikation der Account-Hierarchie.
# Ausgabe wird mit Python pretty-printed (kein jq noetig).
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

PPYTHON='import sys,json; print(json.dumps(json.load(sys.stdin), indent=2, ensure_ascii=False))'

section "Gesamte Employee-Hierarchie E-000004 (rekursiv)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/employees/E-000004"}' | python3 -c "$PPYTHON"

section "Events auf Primary Account PUA-000001"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/primary-accounts/PUA-000001"}' | python3 -c "$PPYTHON"

section "Events auf Secondary Account SUA-000001"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/secondary-accounts/SUA-000001"}' | python3 -c "$PPYTHON"

section "Events auf Admin Account ADM-000001"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/admin-accounts/ADM-000001"}' | python3 -c "$PPYTHON"

section "Admin-Accounts per Employee-Stream (forward)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subjectPrefix":"/employees/","recursive":false,"filter":"event.type == \u0027admin-account.assigned\u0027"}' | python3 -c "$PPYTHON" || echo "(PREFIX-Query wird evtl. nicht unterstuetzt — kann alternativ ueber CEL subject prefix gefiltert werden)"

section "Alle Secondary Accounts mit Owner-Link (bidirektional)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subjectPrefix":"/secondary-accounts/","recursive":false,"filter":"event.type == \u0027owner.assigned\u0027"}' | python3 -c "$PPYTHON" || echo "(Fallback: einzelne Queries pro Account)"

echo
echo "Fertig."
