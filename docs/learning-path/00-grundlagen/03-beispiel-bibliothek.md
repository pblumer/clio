# Grundlagen 3 — Das durchgehende Beispiel

> **Für alle Rollen.** Hier definieren wir die beiden Domänen, die sich durch
> den ganzen Lernpfad ziehen, damit jedes Modul am selben roten Faden lehrt.

## Warum ein durchgehendes Beispiel?

Statt in jedem Modul ein neues, kontextloses Snippet zu zeigen, benutzen wir
**zwei** zusammenhängende Domänen:

- **Bibliothek** — der Hauptfaden. Einfach, anschaulich, deckt Write/Read/
  Observe/Schemas/Queries ab.
- **Bankkonto** — für fortgeschrittene Module zu **Concurrency & Invarianten**
  (Preconditions). Das klassische Event-Sourcing-Lehrbeispiel, weil hier
  Regeln wie „Saldo darf nicht negativ werden" greifbar sind.

## Domäne 1: Bibliothek

Ein Buch durchläuft einen Lebenszyklus. Jeder Schritt ist ein Event im Stream
des Buchs, z. B. `/books/42`.

| Subject | Event-Typ | `data` (Beispiel) | Bedeutung |
|---|---|---|---|
| `/books/42` | `acquired` | `{"title":"Dune","author":"Herbert"}` | Buch in den Bestand aufgenommen |
| `/books/42` | `borrowed` | `{"member":"m-7","due":"2026-07-01"}` | Buch ausgeliehen |
| `/books/42` | `returned` | `{"member":"m-7"}` | Buch zurückgegeben |
| `/books/42` | `retired` | `{"reason":"damaged"}` | Buch ausgemustert |

Die **Hierarchie** der Subjects nutzen wir bewusst:

```
/books            ← alle Bücher (recursive)
/books/42         ← genau ein Buch
/books/42, /books/43, …
```

So liest `recursive` über `/books` den gesamten Bestand, während `/books/42`
nur ein einzelnes Buch betrifft. (Mehr zu `recursive` in
[M02](../module/M02-lesen-und-filtern.md).)

### Typischer Strom eines Buchs

```
acquired  → borrowed → returned → borrowed → returned → retired
```

Daraus lassen sich Fragen beantworten wie „Ist das Buch gerade verfügbar?"
(letztes Event ist `returned`/`acquired`, nicht `borrowed`) oder „Wie oft wurde
es ausgeliehen?" (Anzahl `borrowed`-Events).

## Domäne 2: Bankkonto

Ein Konto lebt unter `/accounts/<id>`, z. B. `/accounts/42`.

| Subject | Event-Typ | `data` (Beispiel) | Bedeutung |
|---|---|---|---|
| `/accounts/42` | `opened` | `{"owner":"Ada"}` | Konto eröffnet |
| `/accounts/42` | `deposited` | `{"amount":100}` | Einzahlung |
| `/accounts/42` | `withdrawn` | `{"amount":30}` | Abhebung |

Hier werden zwei **Invarianten** interessant, die wir mit Preconditions
absichern (siehe [M04](../module/M04-optimistic-concurrency.md)):

1. **Ein Konto wird nur einmal eröffnet** — kein zweites `opened`.
2. **Optimistisches Sperren** — schreibe nur, wenn der Strom seit dem Lesen
   nicht verändert wurde (verhindert „verlorene Updates" bei nebenläufigen
   Abhebungen).

## Lauffähige Skripte

Beide Domänen gibt es als echte, ausführbare curl-Skripte:

- [`examples/bibliothek/`](../../../examples/bibliothek/) — Hauptfaden
- [`examples/bankkonto/`](../../../examples/bankkonto/) — Concurrency/Invarianten

Jedes Skript ist eigenständig lauffähig (Token via `export TOKEN=…`). Die
Module verweisen jeweils auf das passende Skript im **Hands-on**-Teil.

## Checkpoint

1. Welches Subject liest **alle** Bücher auf einmal, und welches Flag brauchst
   du dafür?
2. In der Bibliotheks-Domäne: Wie erkennst du allein aus dem Event-Strom, ob
   ein Buch gerade ausgeliehen ist?
3. Warum eignet sich das Bankkonto besser als die Bibliothek, um Preconditions
   zu demonstrieren?

→ Lösungen in [`uebungen/loesungen.md`](../uebungen/loesungen.md#grundlagen-3).

---

**Weiter:** Zurück zum [Rollen-Wegweiser](../README.md#wähle-deine-rolle) und
wähle deinen Track.
