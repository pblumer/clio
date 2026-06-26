# Identity-Demo: Employees und Mailboxes als separate Aggregate

Dieses Beispiel zeigt, wie in Clio **zwei eigenstaendige Aggregate**
modelliert und ueber Events miteinander verknuepft werden:

- `/employees/<id>`     — das Employee-Aggregat
- `/mailboxes/<id>`     — das Mailbox-Aggregat
- `/primary-accounts/<id>` — Primary User Account
- `/secondary-accounts/<id>` — Secondary User Account (Owner = Primary)
- `/admin-accounts/<id>`   — Admin Account
- `/test-accounts/<id>`    — Test Account

## Architektur-Entscheidung

Statt `mailbox.attached` als bloßes Datenfeld im Employee-Event zu
modellieren, wird die Mailbox als **eigenes Subject** gefuehrt. Die
Beziehung `employee <-> mailbox` entsteht durch ein Event auf *beiden*
Streams:

| Stream | Event-Typ | Bedeutung |
|---|---|---|
| `/employees/E-000001` | `mailbox.attached` | "Dieser Employee hat jetzt diese Mailbox" |
| `/mailboxes/MBX-000001` | `employee.assigned` | "Diese Mailbox gehoert jetzt diesem Employee" |

Das gleiche bidirektionale Muster gilt fuer:

- **Primary Account** <-> `primary-account.assigned` / `employee.assigned`
- **Secondary Account** <-> `secondary-account.linked` / `owner.assigned`
- **Admin Account** <-> `admin-account.assigned` / `employee.assigned`
- **Test Account** <-> `test-account.assigned` / `employee.assigned`

## Business Rules

- Jeder Employee (intern oder extern) hat **mindestens einen** Primary Account.
- Ein Secondary Account braucht **immer einen Primary als Owner**.
- Admin Accounts werden typischerweise nur internen Mitarbeitern zugewiesen.

## Skripte ausfuehren

```bash
export TOKEN=kid_xxxxx.yyyyy    # dein API-Key

# 1. Basis-aggregate (Employee + Mailbox) anlegen
./01-write-entities.sh

# 2. Relation herstellen (atomar)
./02-attach-mailboxes.sh

# 3. Basis-Queries (CEL)
./03-query-relations.sh

# 4. Erweiterte Entitaeten (Primary, Secondary, Admin, Test Accounts)
./04-write-extended-entities.sh

# 5. Hierarchie verknuepfen (atomar je Beziehung)
./05-link-account-hierarchy.sh

# 6. Erweiterte Queries (Hierarchie, Bidirektional)
./06-query-account-hierarchy.sh
```

## CEL-Query-Beispiele

Alle `mailbox.attached`-Events (Join-Logik):
```json
{"subject":"/employees/","recursive":true,"where":"event.type == 'mailbox.attached'"}
```

Alle `employee.assigned`-Events (Gegenrichtung):
```json
{"subject":"/mailboxes/","recursive":true,"where":"event.type == 'employee.assigned'"}
```

Alle Events eines konkreten Employees:
```json
{"subject":"/employees/E-000001","recursive":false}
```

## Teststudio

Im `clio-workbench` gibt es dazu eine **Test-Suite**
`identity-lifecycle-tests`, die die Business Rules als State-Machine
validiert (z. B. Secondary ohne Primary wird verworfen).

Seeehe `examples/teststudio/` im clio-workbench Repo.
