#!/usr/bin/env bash
# I02 — Relation herstellen: mailbox.attached auf dem Employee-Stream
# mit Referenz auf das Mailbox-Aggregat. Beide Sichten in EINEM
# atomaren Write, damit die Beziehung konsistent ist.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Mailbox an Employee anhängen (Muster + Mustermann)"
curl -sS -X POST "${CLIO_BASE}/api/v1/write-events" "${AUTH[@]}" \
  -d '{"events":[
    {"source":"identity","subject":"/employees/E-000001","type":"mailbox.attached","data":{"mailboxId":"MBX-000001","email":"max.muster@example.com"}},
    {"source":"identity","subject":"/employees/E-000002","type":"mailbox.attached","data":{"mailboxId":"MBX-000002","email":"erika.mustermann@example.com"}},
    {"source":"identity","subject":"/mailboxes/MBX-000001","type":"employee.assigned","data":{"employeeId":"E-000001"}},
    {"source":"identity","subject":"/mailboxes/MBX-000002","type":"employee.assigned","data":{"employeeId":"E-000002"}}
  ]}'
echo

section "Beispiel bleibt ohne Mailbox"
echo "(E-000003 hat kein mailbox.attached — dient als Negativ-Fall)"

echo
echo "Fertig. Weiter mit 03-query-relations.sh"
