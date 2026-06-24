#!/usr/bin/env bash
# I04 — Erweiterte Entitäten: Primary/Secondary User Accounts, Admin Accounts,
# Test User Accounts. Employee-Typ (internal/external) wird beim Anlegen
# mitgegeben.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Interner Employee E-000004 mit erweiterten Accounts"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000004","type":"employee.created","data":{"firstName":"Claudia","lastName":"Intern","department":"Engineering","type":"internal"}},
    {"source":"identity","subject":"/primary-accounts/PUA-000001","type":"primary-account.created","data":{"employeeId":"E-000004","username":"cintern","email":"claudia.intern@example.com","status":"active"}},
    {"source":"identity","subject":"/secondary-accounts/SUA-000001","type":"secondary-account.created","data":{"employeeId":"E-000004","ownerPrimaryAccountId":"PUA-000001","username":"cintern-sec","purpose":"shared-inbox","status":"active"}},
    {"source":"identity","subject":"/admin-accounts/ADM-000001","type":"admin-account.created","data":{"employeeId":"E-000004","level":"super","permissions":["user.read","user.write","system.config"]}},
    {"source":"identity","subject":"/test-accounts/TST-000001","type":"test-account.created","data":{"createdBy":"E-000004","scope":"integration","expiryDate":"2026-12-31"}}
  ]}'
echo

section "Externer Employee E-000005 (nur Primary + Secondary)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000005","type":"employee.created","data":{"firstName":"Peter","lastName":"Extern","department":"HR","type":"external"}},
    {"source":"identity","subject":"/primary-accounts/PUA-000002","type":"primary-account.created","data":{"employeeId":"E-000005","username":"pextern","email":"peter.extern@example.com","status":"active"}},
    {"source":"identity","subject":"/secondary-accounts/SUA-000002","type":"secondary-account.created","data":{"employeeId":"E-000005","ownerPrimaryAccountId":"PUA-000002","username":"pextern-sec","purpose":"support","status":"active"}}
  ]}'
echo

section "Isolierter Test-Account ohne Employee (Standalone)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/test-accounts/TST-000002","type":"test-account.created","data":{"scope":"e2e","expiryDate":"2026-11-01"}}
  ]}'
echo

echo
echo "Fertig. Weiter mit 05-link-account-hierarchy.sh"
