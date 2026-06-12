# M06 — Abfragen mit CEL (run-query)

> **Tracks:** Anwendungsentwickler · **Dauer:** ~20 Min

## Lernziele

- Events mit einem **CEL-Prädikat** über `event` filtern.
- Den Scope (`subject`/`recursive`/Bounds/`limit`) mit dem `where`-Prädikat
  kombinieren.
- Auf fehlende Felder robust prüfen (`has(...)`).
- Verstehen, warum Clio CEL statt einer eigenen EventQL-Sprache nutzt.

## Voraussetzungen

- [M02](M02-lesen-und-filtern.md). Ein paar Events mit `data`-Feldern.

## Inhalt

### Warum CEL?

Der Typ-Filter aus [M02](M02-lesen-und-filtern.md) (`types`) reicht für „alle
Events vom Typ X". Sobald du über **Inhalte** filtern willst (`data.amount >
100`), brauchst du ein Prädikat. Clio nutzt
[CEL](https://github.com/google/cel-go) (Common Expression Language) — eine
reife, getestete Engine — statt eine eigene Sprache zu bauen
([ADR-017](../../../ARCHITECTURE.md#adr-017-abfrageschicht-auf-cel-statt-eigener-eventql-sprache)).

### Die Variable `event`

Im Prädikat steht `event` mit den Metadaten (`event.type`, `event.subject`, …)
und den Nutzdaten als dynamische Map (`event.data.*`).

```bash
# Alle Ausleihen eines bestimmten Mitglieds, über alle Bücher
curl -X POST http://127.0.0.1:3000/api/v1/run-query \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true,
       "where":"event.type == '\''borrowed'\'' && has(event.data.member) && event.data.member == '\''m-7'\''"}'
```

Antwort: gefilterte Events als **NDJSON** (wie beim Lesen).

### Robust gegen fehlende Felder: `has(...)`

`has(event.data.x)` schützt vor Events, denen das Feld fehlt. Ein
Auswertungsfehler eines einzelnen Events gilt als **„kein Treffer"** — die
Query bricht nicht ab.

```bash
# Banking-Beispiel: große Abhebungen
curl -X POST http://127.0.0.1:3000/api/v1/run-query \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/accounts","recursive":true,
       "where":"event.type == '\''withdrawn'\'' && has(event.data.amount) && event.data.amount > 100"}'
```

### Scope + Limit

Alle Lese-Optionen gelten weiter: `subject`, `recursive`, `lowerBound`/
`upperBound`, plus optionales `limit`. Das Prädikat filtert *innerhalb* des
Scopes — wähle den Scope eng, dann muss CEL weniger Events prüfen.

### Verwandt: Query-Preconditions

Dasselbe CEL-Prädikat steckt in `isQueryResultEmpty`/`isQueryResultNonEmpty`
(siehe [M04](M04-optimistic-concurrency.md)) — Abfrage zum Lesen *und* als
Schreibbedingung.

### Bewusste Grenze

`run-query` ist **keine** EventQL-Reimplementierung — keine Byte-Kompatibilität
zu EventSourcingDB, dafür ein Bruchteil des Aufwands. Aggregation/Grouping und
Projektionen sind noch offen (Roadmap Stufe 4, Etappen 4–5).

## Hands-on

Skript: [`examples/bibliothek/06-cel-query.sh`](../../../examples/bibliothek/06-cel-query.sh)

Zeigt Filter über `event.type` und `event.data`, inkl. `has(...)`.

## Checkpoint

1. Warum solltest du `has(event.data.amount)` vor `event.data.amount > 100`
   schreiben?
2. Was passiert mit einem Event, dessen Prädikat-Auswertung einen Fehler wirft?
3. Wie hängen `run-query` und die Query-Precondition zusammen?

→ [Lösungen](../uebungen/loesungen.md#m06)

---

**Weiter:** [M07 — Integrität & Signaturen](M07-integritaet-und-signaturen.md)
