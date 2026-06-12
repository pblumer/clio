# Track: Anwendungsentwickler:in 🛠️

> Du willst eine Anwendung gegen Clio bauen — Events schreiben, Streams lesen,
> live reagieren, Invarianten absichern und Daten abfragen. Dieser Track macht
> dich mit der **kompletten HTTP-API** vertraut.

## Dein Ziel

Du kannst eine event-gesourcte Anwendung gegen Clio entwerfen: vom ersten Event
über Optimistic Concurrency bis zu Schema-Verträgen und CEL-Abfragen.

## Voraussetzungen

- [Grundlagen 1–3](../00-grundlagen/) abgeschlossen (Konzept + Quickstart +
  Beispiel-Domänen).
- `curl` und ein Texteditor; JSON sollte dir vertraut sein.

## Reihenfolge

| # | Modul | Du lernst… |
|---|---|---|
| 1 | [M01 — Erstes Event](../module/M01-erstes-event.md) | Event-Aufbau (CloudEvents), atomares Schreiben mehrerer Events |
| 2 | [M02 — Lesen & Filtern](../module/M02-lesen-und-filtern.md) | `recursive`, `lowerBound`/`upperBound`, `types`, GET-Komfortroute |
| 3 | [M03 — Live beobachten](../module/M03-live-observe.md) | `observe-events`, Reconnect via `lowerBound` |
| 4 | [M04 — Optimistic Concurrency](../module/M04-optimistic-concurrency.md) | Preconditions, 409, Konto-Invarianten (Bankkonto-Beispiel) |
| 5 | [M05 — Event-Schemas](../module/M05-schemas.md) | JSON-Schema-Verträge je Typ, Validierung beim Write |
| 6 | [M06 — CEL-Abfragen](../module/M06-cel-queries.md) | `run-query` mit Prädikaten über `event.data` |
| 7 | [M07 — Integrität & Signaturen](../module/M07-integritaet-und-signaturen.md) | `verify`, Hash-Kette, Ed25519 — als Client nutzen |

## Querschnittsthemen, die du mitnehmen solltest

- **Idempotenz & Ordnung:** Event-IDs sind global monoton — nutze sie als
  Cursor (Reconnect/Replay), nicht eigene Zähler.
- **Subjects als Aggregat-Grenze:** Ein Stream pro Aggregat (`/books/42`,
  `/accounts/42`). Invarianten gelten pro Stream → Preconditions pro Stream.
- **Projektionen baust du selbst:** Clio liefert Events; Lesemodelle leitest du
  in deiner App ab (Non-Goal von Clio, siehe
  [`ARCHITECTURE.md` §2.3](../../../ARCHITECTURE.md#23-nicht-ziele-non-goals)).

## Hands-on

Alle Module verweisen auf lauffähige Skripte:
[`examples/bibliothek/`](../../../examples/bibliothek/) (Hauptfaden) und
[`examples/bankkonto/`](../../../examples/bankkonto/) (Concurrency/Invarianten).

## Geschafft, wenn…

Du eine kleine Domäne modellieren kannst: Welche Subjects, welche Event-Typen,
welche Invarianten via Precondition, welches Schema je Typ — und du es mit
`curl` end-to-end durchspielen kannst.
