# M02 — Lesen & Filtern

> **Tracks:** Einsteiger, Anwendungsentwickler · **Dauer:** ~20 Min

## Lernziele

- Einen Stream lesen und das **NDJSON**-Antwortformat verstehen.
- Mit `recursive` ganze Teilbäume lesen (`/books` statt `/books/42`).
- Mit `lowerBound`/`upperBound` einen **ID-Bereich** lesen.
- Mit `types` nach Event-Typen filtern.
- Die ergonomische **GET-Komfortroute** nutzen.

## Voraussetzungen

- [M01](M01-erstes-event.md). Ein paar Events in `/books/...` vorhanden.

## Inhalt

### Grundform

```bash
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42"}'
```

Antwort: **NDJSON** — ein JSON-Event pro Zeile, in **globaler Schreibreihenfolge**.

### Rekursiv: ganze Teilbäume

`recursive: true` bezieht alle Unter-Subjects ein. Die Hierarchie der Subjects
macht das nützlich:

```bash
# Alle Bücher (alles unterhalb von /books)
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true}'
```

`/` + `recursive` = **alle Events des Systems**. Rekursive Reads laufen über den
globalen Event-Bucket und bewahren so die globale Ordnung
([ADR-005](../../../ARCHITECTURE.md#adr-005-subjects-als-hierarchische-stream-identifier)).

### ID-Bereich: lowerBound / upperBound

Beide Grenzen sind **inklusive**. Damit liest du z. B. „verpasste" Events ab
einem bekannten Cursor nach:

```bash
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42","lowerBound":"2","upperBound":"10"}'
```

### Nach Event-Typ filtern

```bash
# Nur Ausleihen/Rückgaben, rekursiv über alle Bücher
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true,"types":["borrowed","returned"]}'
```

`types` ist mit `recursive` und den Bounds kombinierbar (leer/weggelassen =
alle Typen). Für „alle Events vom Typ X" reicht das oft schon ohne CEL
([ADR-007](../../../ARCHITECTURE.md#adr-007-eventql-als-spätes-optionales-ziel)).

### GET-Komfortroute (ergonomischer für Tools/curl)

Subject steht direkt im Pfad, Optionen als Query. Hier ist `recursive`
**default `true`**:

```bash
# Ein Stream
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/events/books/42

# Eltern-Pfad: automatisch alles darunter
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/events/books

# Mit Optionen (type wiederholbar)
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/events/books?type=borrowed&lowerBound=2"
```

Details: [ADR-010](../../../ARCHITECTURE.md#adr-010-komfort-leseroute-get-apiv1eventssubject).

## Hands-on

Skript: [`examples/bibliothek/02-lesen-und-filtern.sh`](../../../examples/bibliothek/02-lesen-und-filtern.sh)

Es zeigt Einzel-Stream, rekursiv, Bounds, Typ-Filter und die GET-Route
nebeneinander.

## Checkpoint

1. Was liefert `{"subject":"/","recursive":true}`?
2. Worin unterscheidet sich die GET-Route bei `recursive` von `read-events`?
3. Du kennst die letzte verarbeitete Event-ID `41`. Wie liest du nur die neuen
   Events?

→ [Lösungen](../uebungen/loesungen.md#m02)

---

**Weiter:** [M03 — Live beobachten](M03-live-observe.md)
