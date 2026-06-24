#!/usr/bin/env bash
# I01 — Aggregate anlegen: Employee-Instanzen und Mailbox-Instanzen als
# eigenständige Subjects (/employees/* und /mailboxes/*).
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "3 Employees anlegen"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000001","type":"employee.created","data":{"firstName":"Max","lastName":"Muster","department":"Engineering"}},
    {"source":"identity","subject":"/employees/E-000002","type":"employee.created","data":{"firstName":"Erika","lastName":"Mustermann","department":"HR"}},
    {"source":"identity","subject":"/employees/E-000003","type":"employee.created","data":{"firstName":"Hans","lastName":"Beispiel","department":"Sales"}}
  ]}'
echo

section "3 Mailboxes anlegen (noch unverbunden)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/mailboxes/MBX-000001","type":"mailbox.created","data":{"email":"max.muster@example.com","quotaMb":5120}},
    {"source":"identity","subject":"/mailboxes/MBX-000002","type":"mailbox.created","data":{"email":"erika.mustermann@example.com","quotaMb":2048}},
    {"source":"identity","subject":"/mailboxes/MBX-000003","type":"mailbox.created","data":{"email":"hans.beispiel@example.com","quotaMb":1024}}
  ]}'
echo

echo
echo "Fertig. Weiter mit 02-attach-mailboxes.sh"
