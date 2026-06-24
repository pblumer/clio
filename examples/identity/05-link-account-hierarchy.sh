#!/usr/bin/env bash
# I05 — Bidirektionale Relationen auf dem Event Store:
# Employee <-> Primary <-> Secondary <-> Admin/Test
# Alles in atomaren Writes je Aggregate, damit die Konsistenz sichergestellt ist.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Employee E-000004 mit Primary Account verknuepfen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000004","type":"primary-account.assigned","data":{"primaryAccountId":"PUA-000001","linkedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/primary-accounts/PUA-000001","type":"employee.assigned","data":{"employeeId":"E-000004"}}
  ]}'
echo

section "Primary PUA-000001 mit Secondary SUA-000001 verknuepfen (Owner)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/primary-accounts/PUA-000001","type":"secondary-account.linked","data":{"secondaryAccountId":"SUA-000001","linkedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/secondary-accounts/SUA-000001","type":"owner.assigned","data":{"ownerPrimaryAccountId":"PUA-000001"}}
  ]}'
echo

section "Employee E-000004 mit Admin Account verknuepfen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000004","type":"admin-account.assigned","data":{"adminAccountId":"ADM-000001","grantedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/admin-accounts/ADM-000001","type":"employee.assigned","data":{"employeeId":"E-000004"}}
  ]}'
echo

section "Employee E-000004 mit Test Account verknuepfen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000004","type":"test-account.assigned","data":{"testAccountId":"TST-000001","linkedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/test-accounts/TST-000001","type":"employee.assigned","data":{"employeeId":"E-000004"}}
  ]}'
echo

section "Employee E-000005 (External) mit Primary und Secondary verknuepfen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000005","type":"primary-account.assigned","data":{"primaryAccountId":"PUA-000002","linkedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/primary-accounts/PUA-000002","type":"employee.assigned","data":{"employeeId":"E-000005"}},
    {"source":"identity","subject":"/primary-accounts/PUA-000002","type":"secondary-account.linked","data":{"secondaryAccountId":"SUA-000002","linkedAt":"2026-06-24T15:00:00Z"}},
    {"source":"identity","subject":"/secondary-accounts/SUA-000002","type":"owner.assigned","data":{"ownerPrimaryAccountId":"PUA-000002"}}
  ]}'
echo

echo
echo "Fertig. Weiter mit 06-query-account-hierarchy.sh"
