# Identity-Demo: Employees und Mailboxes als separate Aggregate

Dieses Beispiel zeigt, wie in Clio **zwei eigenständige Aggregate**
modelliert und über Events miteinander verknüpft werden:

- `/employees/<id>`   — das Employee-Aggregat
- `/mailboxes/<id>`   — das Mailbox-Aggregat

## Architektur-Entscheidung

Statt `mailbox.attached` als bloßes Datenfeld im Employee-Event zu
modellieren, wird die Mailbox als **eigenes Subject** geführt. Die
Beziehung `employee → mailbox` entsteht durch ein Event auf *beiden*
Streams:

| Stream | Event-Typ | Bedeutung |
|---|---|---|
| `/employees/E-000001` | `mailbox.attached` | "Dieser Employee hat jetzt diese Mailbox" |
| `/mailboxes/MBX-000001` | `employee.assigned` | "Diese Mailbox gehört jetzt diesem Employee" |

Beide Events werden in **einem atomaren Write** geschrieben, damit die
Beziehung nie halb-fertig existiert.

## Skripte ausführen

```bash
export TOKEN=kid_xxxxx.yyyyy    # dein API-Key

# 1. Aggregate anlegen
./01-write-entities.sh

# 2. Relation herstellen (atomar)
./02-attach-mailboxes.sh

# 3. Mit CEL-Queries abfragen
./03-query-relations.sh
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
