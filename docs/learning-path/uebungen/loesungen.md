# Musterlösungen

Lösungen zu den Checkpoints. Schau erst selbst — das Ausprobieren gegen eine
laufende Instanz ist der eigentliche Lerneffekt.

---

## Grundlagen 1

1. Z. B.: *„Wie war der Kontostand letzten Dienstag?"* und *„In welcher
   Reihenfolge und warum kam es zum aktuellen Stand?"* — beides braucht die
   Historie, die ein reiner Zustands-Speicher überschreibt.
2. Das **Subject** ist der *Identifier* (der Pfad, z. B. `/books/42`); der
   **Stream** ist die *geordnete Folge der Events* zu diesem Subject. Subject =
   Adresse, Stream = Inhalt.
3. Globale Monotonie gibt eine **systemweite, strikte Ordnung** — wichtig für
   rekursives Lesen/Beobachten über mehrere Streams und für einen verlässlichen
   Cursor (Reconnect/Replay). Pro-Stream-Eindeutigkeit allein erlaubt keine
   globale Reihenfolge.

## Grundlagen 2

1. `read-events` ist eine **geschützte Datenroute** (Bearer-Token-Middleware);
   `ping` ist bewusst die einzige ungeschützte Erreichbarkeitsprüfung.
2. `id`, `time`, `specversion` (zusätzlich `hash`/`predecessorhash`, und
   `signature` falls Signing aktiv).
3. In **Schreibreihenfolge** (aufsteigende, global monotone IDs): zuerst
   `acquired`, dann `borrowed`.

## Grundlagen 3

1. `/books` mit `recursive: true` (bzw. die GET-Route `…/events/books`, wo
   `recursive` default `true` ist).
2. Wenn das **letzte** Event des Streams `borrowed` ist (und kein späteres
   `returned`/`retired` folgt), ist das Buch ausgeliehen.
3. Beim Bankkonto sind **Invarianten** natürlich („nur einmal eröffnen", „Saldo
   nicht negativ") — sie verlangen Bedingungen beim Schreiben, also genau das,
   was Preconditions absichern.

---

## M01

1. Du lieferst `source`, `subject`, `type`, optional `data`. Der Server ergänzt
   `id`, `time`, `specversion`, `hash`/`predecessorhash` (und `signature`).
2. **Null.** Der Write ist atomar (alles-oder-nichts) in einer Transaktion —
   ein ungültiges Event lässt den ganzen Aufruf scheitern.
3. CloudEvents definiert `id` als **String**; das hält das Format einheitlich
   und vermeidet Zahlentyp-/Überlauf-Fragen, obwohl Clio numerisch-monotone IDs
   vergibt.

## M02

1. **Alle Events des gesamten Systems**, in globaler Schreibreihenfolge.
2. Bei der GET-Komfortroute ist `recursive` **default `true`** (passend zum
   Pfad-Browsing); bei `read-events` ist es default `false`.
3. `{"subject":"…","lowerBound":"42"}` — `lowerBound` ist inklusive; willst du
   *streng* nach 41, nimm `lowerBound:"42"` (da 41 die letzte verarbeitete war).

## M03

1. Zuerst die passende **History** (wie ein Read im selben Scope), dann bleibt
   die Verbindung für neue Events offen.
2. Reconnect mit `lowerBound:"42"` (bzw. der nächsten ID) — verpasste Events
   werden nachgeliefert; Dedup über die Event-ID verhindert Duplikate.
3. Um den **Schreibpfad nicht zu blockieren**: ein langsamer Leser darf Writes
   nicht ausbremsen. Er wird abgehängt und holt per `lowerBound` auf.

## M04

1. *Optimistic*, weil **nicht vorab gesperrt** wird: man schreibt hoffnungsvoll
   und lässt die Bedingung **beim Commit** prüfen. Konflikte sind selten →
   günstiger als Sperren (pessimistic).
2. Den Stream **neu lesen**, die Geschäftsregel mit dem aktuellen Zustand neu
   prüfen, und mit aktualisierter Precondition (neue letzte Event-ID) **erneut
   versuchen** (Retry-Schleife).
3. `isSubjectOnEventId` für **optimistisches Sperren** (Schutz vor verlorenen
   Updates, unabhängig vom Inhalt). `isQueryResultEmpty` für **inhaltliche**
   Bedingungen über `event.data`/`event.type`.

## M05

1. **Nein.** Schemas sind unveränderlich (erneute Registrierung → 409). Das
   schützt die Historie; Lockern würde bestehende Garantien brechen.
2. **409** = es existiert bereits ein Schema für den Typ (Unveränderlichkeit).
   **400-bei-Registrierung** = die bereits gespeicherte Historie des Typs ist
   nicht konform, das Schema wird deshalb abgelehnt.
3. `read-event-types` — es zeigt pro Typ `hasSchema`.

## M06

1. CEL-Auswertung ist robuster: `has(...)` verhindert Fehler bei Events ohne
   das Feld; ohne den Guard würde die Auswertung dieses Events fehlschlagen
   (gilt als „kein Treffer").
2. Es gilt als **kein Treffer** — die Query läuft weiter, statt abzubrechen.
3. Beides nutzt dasselbe CEL-Prädikat über `event`: `run-query` zum **Lesen**,
   `isQueryResultEmpty`/`NonEmpty` als **Schreibbedingung** (Precondition).

## M07

1. Die **Hash-Kette** beweist *Integrität* (die Historie wurde nicht
   nachträglich geändert), die **Signatur** beweist *Authentizität* (Urheber).
   Integrität ohne Authentizität sagt nicht, *von wem*; Authentizität braucht
   eine unveränderte Basis.
2. **Trennung der Belange:** so bricht ein verfälschter Signatur-Wert nur die
   Signaturprüfung, nicht die Integritätskette — beide Eigenschaften bleiben
   unabhängig prüfbar.
3. Als **periodischer Check** (Cron/Monitoring). `ok:false,brokenAt:"57"`
   bedeutet: ab Event 57 stimmt die Kette nicht mehr — Hinweis auf Manipulation
   oder Korruption; Backup/Forensik nötig.

## M08

1. `always` — bei einzelnen, sequentiellen, latenzkritischen Writes ist die
   Batch-Verzögerung von `group` nachteilig; `always` hat die geringste
   Einzel-Latenz bei voller Durability.
2. Wegen des **Datei-Locks**: eine laufende Instanz hält die bbolt-Datei;
   `compact` braucht exklusiven Zugriff für den atomaren Swap.
3. Die Daten liegen nur im **Container-Schreiblayer** und sind nach dem
   Neustart/Entfernen **weg** — ohne gemountetes Volume keine Persistenz.

## M09

1. Geringe **Kardinalität**: variable Pfade (`/api/v1/events/<beliebig>`) würden
   sonst unendlich viele Label-Werte erzeugen; das Mux-Pattern gruppiert sie.
2. `clio_db_size_bytes` (zusammen mit `clio_events_total` als Kontext).
3. Möglicherweise **nicht geschlossene Observe-Verbindungen** (Client-Leck) oder
   schlicht viele aktive Live-Konsumenten — im Zweifel untersuchen.

## M10

1. Hash-Kette: `internal/store` (Kern in `store.go`, Signaturen in
   `store_signing.go`). CEL-Auswertung: `internal/query/query.go`.
2. Nur so ist die Prüfung **atomar** mit dem Write: zwischen „prüfen" und
   „schreiben" darf kein anderer Write dazwischenkommen — sonst wäre die
   Bedingung nicht mehr garantiert (Race).
3. Mindestens die **OpenAPI-Spec** (`internal/apidocs/openapi.yaml`); je nach
   Route auch Tests in `server_test.go` und ein ADR.

## M11

1. **Code**, **Tests**, **OpenAPI-Update** (bei API-Änderung), **ADR +
   Roadmap-/Versionspflege**, und `gofmt`.
2. Er wird **nicht gelöscht**, sondern auf Status „Abgelöst durch ADR-XYZ"
   gesetzt — die Entscheidungshistorie bleibt erhalten.
3. Weil **Abhängigkeitsarmut** ein Designziel ist (ADR-001): jede Dependency
   vergrößert das Binary und die Angriffs-/Wartungsfläche — daher nur bewusst
   und begründet (wie bbolt, cel-go, jsonschema).
