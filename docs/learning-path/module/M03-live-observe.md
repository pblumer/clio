# M03 — Live beobachten (observe-events)

> **Tracks:** Einsteiger, Anwendungsentwickler · **Dauer:** ~20 Min

## Lernziele

- Verstehen, was `observe-events` von `read-events` unterscheidet.
- Einen Stream live beobachten (erst History, dann offene Verbindung).
- Nach einem Verbindungsabbruch korrekt **reconnecten** (via `lowerBound`).
- Das Verhalten bei langsamen Konsumenten einordnen.

## Voraussetzungen

- [M02](M02-lesen-und-filtern.md). Zwei Terminals sind hilfreich.

## Inhalt

### Das „Aha" von Event Sourcing

`observe-events` liefert zuerst die passende **History** (wie ein Read) und hält
die Verbindung dann **offen**: neue Events werden sofort als NDJSON
nachgeliefert. So reagieren nachgelagerte Systeme in Echtzeit, ohne zu pollen.

```bash
# Terminal A: live alles unterhalb von /books beobachten (-N = ungepuffert)
curl -N -X POST http://127.0.0.1:3000/api/v1/observe-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true}'
```

```bash
# Terminal B: ein neues Event schreiben
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/99","type":"acquired","data":{"title":"Neu"}}]}'
```

In Terminal A erscheint das Event **sofort**. Optionen wie `recursive`,
`lowerBound` und `types` gelten hier genauso wie beim Lesen.

### Reconnect: keine Events verpassen

Bricht die Verbindung ab, verbindest du dich mit `lowerBound` ab der **letzten
gesehenen ID** neu — die verpasste History wird nachgeliefert, dann läuft der
Live-Strom weiter. Die global monotonen Event-IDs sind dein **Cursor**:

```bash
curl -N -X POST http://127.0.0.1:3000/api/v1/observe-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true,"lowerBound":"42"}'
```

### Langsame Konsumenten

Pub/Sub läuft über Channels pro Verbindung. **Langsame Subscriber werden
abgehängt** (→ Reconnect), statt den Schreibpfad zu blockieren — Writes bleiben
schnell, der Konsument holt per `lowerBound` auf
([Stufe 2](../../../ARCHITECTURE.md#stufe-2--observe--live-streaming-)).

### Auch per GET

```bash
curl -N -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/events/books?watch=true"
```

## Hands-on

Skript: [`examples/bibliothek/03-observe.sh`](../../../examples/bibliothek/03-observe.sh)

Es startet einen Observer im Hintergrund, schreibt dann Events und zeigt, wie
sie live eintreffen.

## Checkpoint

1. Was bekommst du bei `observe-events` *zuerst*, bevor neue Events kommen?
2. Du warst kurz offline und kennst Event-ID `42` als letzte gesehene. Wie
   reconnectest du ohne Lücke und ohne Duplikate?
3. Warum hängt Clio einen zu langsamen Observer ab, statt zu warten?

→ [Lösungen](../uebungen/loesungen.md#m03)

---

**Weiter:** [M04 — Optimistic Concurrency](M04-optimistic-concurrency.md)
