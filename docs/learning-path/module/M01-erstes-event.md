# M01 — Erstes Event schreiben

> **Tracks:** Einsteiger, Anwendungsentwickler · **Dauer:** ~15 Min

## Lernziele

- Den Aufbau eines Events (CloudEvents) verstehen: welche Felder *du* lieferst
  und welche der **Server** ergänzt.
- Ein einzelnes und **mehrere Events atomar** schreiben.
- Die Antwort des Servers lesen (inkl. `id`, `time`, `hash`).

## Voraussetzungen

- [Grundlagen 1–3](../00-grundlagen/). Eine laufende Instanz und `export TOKEN=…`.

## Inhalt

### Anatomie eines Event-Candidate

Du sendest einen **Event Candidate** — einen Vorschlag. Erst wenn Clio ihn
annimmt, wird daraus ein echtes Event. Du lieferst:

| Feld | Pflicht | Bedeutung |
|---|---|---|
| `source` | ja | Wer das Event erzeugt hat (z. B. `library`, eine URI) |
| `subject` | ja | Der Stream-Pfad, beginnt mit `/` (z. B. `/books/42`) |
| `type` | ja | Was passiert ist (z. B. `acquired`) |
| `data` | optional | Die Nutzdaten als JSON-Objekt |

Der **Server ergänzt** automatisch:

- `id` — global monoton steigend, eindeutig (String),
- `time` — Zeitstempel der Annahme,
- `specversion` — CloudEvents-Version,
- `hash`/`predecessorhash` — Tamper-Evidence-Kette (siehe [M07](M07-integritaet-und-signaturen.md)),
- `signature` — `null`, außer ein Signing-Key ist konfiguriert.

> Hintergrund: CloudEvents-Format ([ADR-004](../../../ARCHITECTURE.md#adr-004-cloudevents-als-event-format-strukturiertes-json)),
> Subjects als Stream-IDs ([ADR-005](../../../ARCHITECTURE.md#adr-005-subjects-als-hierarchische-stream-identifier)).

### Ein Event schreiben

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/42","type":"acquired","data":{"title":"Dune","author":"Herbert"}}]}'
```

### Mehrere Events atomar

`events` ist ein **Array**. Alle Events eines Aufrufs werden in **einer**
Transaktion geschrieben — **alles oder nichts**. Schlägt eines fehl (oder eine
Precondition), wird *keines* gespeichert.

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[
        {"source":"library","subject":"/books/42","type":"borrowed","data":{"member":"m-7"}},
        {"source":"library","subject":"/books/42","type":"returned","data":{"member":"m-7"}}
      ]}'
```

Atomares Mehrfach-Schreiben und die monotone, serialisierte ID-Vergabe ergeben
sich aus der einzigen Schreibtransaktion
([ADR-003](../../../ARCHITECTURE.md#adr-003-serialisierte-schreibvorgänge-für-ordnung--atomarität)).

## Hands-on

Skript: [`examples/bibliothek/01-events-schreiben.sh`](../../../examples/bibliothek/01-events-schreiben.sh)

```bash
export TOKEN=dein-token
examples/bibliothek/01-events-schreiben.sh
```

Es legt ein Buch an und schreibt einen kleinen Lebenszyklus
(`acquired → borrowed → returned`).

## Checkpoint

1. Welche Felder ergänzt der Server, welche musst du liefern?
2. Du schickst zwei Events, das zweite hat ein ungültiges Feld. Wie viele Events
   landen im Store? Warum?
3. Warum sind Event-IDs Strings, obwohl sie numerisch aussehen?

→ [Lösungen](../uebungen/loesungen.md#m01)

---

**Weiter:** [M02 — Lesen & Filtern](M02-lesen-und-filtern.md)
