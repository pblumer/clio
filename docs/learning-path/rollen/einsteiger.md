# Track: Einsteiger:in / Neugierig 🧭

> Du hast noch nie mit Event Sourcing oder einem Event Store gearbeitet? Hier
> bist du richtig. Dieser Track gibt dir das Konzept **und** ein erstes
> Erfolgserlebnis, ohne dich in Details zu ertränken.

## Dein Ziel

Nach diesem Track kannst du erklären, *was* Event Sourcing ist und *wofür* Clio
gut ist — und du hast selbst Events geschrieben, gelesen und live beobachtet.

## Voraussetzungen

- Ein Terminal und `curl`. Clio holst du dir als **fertiges Binary** von der
  [Releases-Seite](https://github.com/pblumer/clio/releases/latest) — **kein Go
  nötig** (Details im Quickstart). Sonst nichts.

## Reihenfolge

| # | Schritt | Worum es geht |
|---|---|---|
| 1 | [Grundlagen 1 — Was ist Event Sourcing?](../00-grundlagen/01-was-ist-event-sourcing.md) | Das Konzept, ganz ohne Code |
| 2 | [Grundlagen 2 — Quickstart](../00-grundlagen/02-clio-quickstart.md) | Holen (Binary o. bauen), starten, erstes Event |
| 3 | [Grundlagen 3 — Das Beispiel](../00-grundlagen/03-beispiel-bibliothek.md) | Die Bibliotheks-Domäne kennenlernen |
| 4 | [M01 — Erstes Event](../module/M01-erstes-event.md) | Events bewusst schreiben & verstehen |
| 5 | [M02 — Lesen & Filtern](../module/M02-lesen-und-filtern.md) | Streams lesen, `recursive`, Filter |
| 6 | [M03 — Live beobachten](../module/M03-live-observe.md) | Events in Echtzeit sehen (das „Aha"!) |

> **Lieber klicken statt `curl`?** [Grundlagen 4 — Postman & Tests](../00-grundlagen/04-postman-und-tests.md)
> zeigt denselben Weg in Postman — optional, jederzeit einschiebbar.

## Wenn du Lust auf mehr hast

- Dich reizt das **Bauen von Apps**? → weiter mit dem
  [Anwendungsentwickler-Track](anwendungsentwickler.md).
- Dich interessiert das **große Bild & Trade-offs**? → schau in den
  [Architekt-Track](architekt.md).

## Geschafft, wenn…

Du diese drei Fragen beantworten kannst:

1. Warum speichert ein Event Store Ereignisse statt Zuständen?
2. Was ist ein Subject, und wie hängt `recursive` damit zusammen?
3. Was passiert bei `observe-events`, das bei `read-events` **nicht** passiert?
