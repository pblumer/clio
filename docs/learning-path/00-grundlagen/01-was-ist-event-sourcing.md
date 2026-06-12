# Grundlagen 1 — Was ist Event Sourcing?

> **Für alle Rollen.** Keine Vorkenntnisse nötig. Kein Code — nur Konzept.

## Lernziele

Nach diesem Modul kannst du:

- den Unterschied zwischen **Zustand speichern** und **Ereignisse speichern** erklären,
- die Begriffe **Event**, **Stream**, **Subject**, **Replay**, **Append-only** in eigenen Worten wiedergeben,
- begründen, *warum* man einen dedizierten Event Store wie Clio einsetzt.

## Die Kernidee

Klassische Anwendungen speichern den **aktuellen Zustand**. Überweist du Geld,
wird der Kontostand einfach überschrieben: aus `100 €` wird `70 €`. Die
*Information, dass und warum* sich etwas geändert hat, ist danach weg.

Event Sourcing dreht das um: Gespeichert werden nicht Zustände, sondern die
**Ereignisse** (Events), die zum Zustand geführt haben — als unveränderliche,
streng geordnete Liste, an die nur **hinten angehängt** wird (*append-only*):

```
KontoEröffnet(Saldo=0)
Eingezahlt(Betrag=100)
Abgehoben(Betrag=30)
```

Der **aktuelle Zustand** (`Saldo = 70`) wird bei Bedarf berechnet, indem man
die Events von vorne **erneut abspielt** (*Replay*). Der Zustand ist eine
*Ableitung*, die Events sind die *Wahrheit*.

## Warum macht man das?

- **Vollständige Historie.** Du weißt nicht nur *was* gilt, sondern *wie* es
  dazu kam. Audit, Nachvollziehbarkeit, „Zeitreisen" in alte Zustände.
- **Keine Informationsverluste.** Überschreiben löscht Geschichte; Anhängen nie.
- **Mehrere Lesemodelle.** Aus demselben Event-Strom lassen sich beliebig viele
  Sichten (Projektionen) ableiten — auch nachträglich für neue Anforderungen.
- **Entkopplung.** Andere Systeme können den Strom *beobachten* und reagieren,
  ohne die schreibende Anwendung zu kennen.

## Die wichtigsten Begriffe

| Begriff | Bedeutung |
|---|---|
| **Event** | Ein unveränderliches Faktum über eine Zustandsänderung („Buch ausgeliehen"). In Clio im [CloudEvents](https://cloudevents.io/)-Format. |
| **Subject** | Hierarchischer Pfad, der einen **Stream** identifiziert, z. B. `/books/42`. Beginnt immer mit `/`. |
| **Stream** | Die geordnete Folge aller Events eines Subjects. |
| **Event-ID** | Global monoton steigende, eindeutige Kennung — die Grundlage der **strikten Ordnung**. (Laut CloudEvents ein String, auch wenn er numerisch aussieht.) |
| **Append-only** | An den Strom wird nur angehängt; nichts wird geändert oder gelöscht. |
| **Replay** | Erneutes Lesen der Events, um Zustand zu rekonstruieren. |
| **Observe** | Wie Lesen, aber die Verbindung bleibt offen und neue Events kommen live nach. |
| **Precondition** | Bedingung, die vor einem Schreibvorgang erfüllt sein muss (Optimistic Concurrency). |

> Die vollständige Begriffstabelle steht in
> [`ARCHITECTURE.md` §3](../../../ARCHITECTURE.md#3-fachliche-grundlagen--domänenbegriffe).

## Wo Clio hineinpasst

Clio ist **der Event Store** — die Schicht, die Events sicher, geordnet und
unveränderlich aufbewahrt und sie zum Lesen/Beobachten herausgibt. Bewusst
**nicht** Teil von Clio (Non-Goals):

- **Keine Projektionen** — Lesemodelle baut deine Anwendung.
- **Keine Geschäftslogik** — Clio führt keinen Code aus.
- **Kein Clustering** — Clio läuft als einzelne Instanz.

Diese Abgrenzung ist Absicht und hält Clio klein und korrekt. Mehr dazu in
[`ARCHITECTURE.md` §2.3](../../../ARCHITECTURE.md#23-nicht-ziele-non-goals).

## Checkpoint

1. Eine Banking-App speichert nur den aktuellen Kontostand. Nenne **zwei**
   Fragen, die sie *nicht* beantworten kann, ein Event-Sourcing-System aber
   schon.
2. Was ist der Unterschied zwischen einem **Subject** und einem **Stream**?
3. Warm sind Event-IDs **global monoton** und nicht nur pro Stream eindeutig?
   (Tipp: Was bedeutet „strikte Ordnung über das ganze System"?)

→ Lösungen in [`uebungen/loesungen.md`](../uebungen/loesungen.md#grundlagen-1).

---

**Weiter:** [Grundlagen 2 — Clio Quickstart](02-clio-quickstart.md)
