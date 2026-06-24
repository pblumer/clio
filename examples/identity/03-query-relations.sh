#!/usr/bin/env bash
# I03 — Relationen abfragen: CEL-Queries über /employees und /mailboxes
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Alle Employees mit mailbox.attached (Join über Event-Typ)"
echo "Filter auf /employees: nur Events vom Typ mailbox.attached"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/employees/","recursive":true,"where":"event.type == \u0027mailbox.attached\u0027"}' | jq .

section "Alle Mailboxen, die einem Employee zugewiesen sind"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/mailboxes/","recursive":true,"where":"event.type == \u0027employee.assigned\u0027"}' | jq .

section "Employee E-000001 mit allen Events (inkl. mailbox.attached)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/employees/E-000001","recursive":false}' | jq .

section "Mailbox MBX-000001 mit allen Events (inkl. employee.assigned)"
curl -sS -X POST "${CLIO_BASE}/api/v1/run-query" "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"subject":"/mailboxes/MBX-000001","recursive":false}' | jq .

echo
echo "Fertig."
