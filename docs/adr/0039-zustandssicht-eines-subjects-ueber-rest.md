# ADR-039: Gefaltete Zustandssicht eines Subjects über REST (`GET /state/<subject>`)

**Status:** Akzeptiert (2026-06-26)

**Datum:** 2026-06-26

**Kontext**

Ein Subject ist in der Praxis oft eine **Entität**, die auf dem Zeitstrahl Events
erfährt (ADR-005: Subjects als hierarchische Stream-Identifier). Der Kernwert des
Event-Sourcings ist, dass sich **jede** Veränderung nachvollziehen lässt — der
aktuelle Zustand entsteht laut Grundsatz durch erneutes Abspielen der Events
(`ARCHITECTURE.md` §1). Genau dieses Falten musste bisher **jeder Client selbst**
tun: `read-events`/`GET /events/<subject>` liefern den rohen Event-Strom,
`run-query` (ADR-017) filtert Events — beide geben **Events** zurück, nie einen
**gefalteten Zustand**.

Der häufige, banale Bedarf „gib mir einfach den jetzigen Stand dieser Entität und
die Daten, die zu ihr vorliegen" war damit nur über clientseitige Logik oder ein
externes Read-Model erreichbar. Das ist eine echte Lücke für den Lesepfad.

Gleichzeitig gibt es eine **bewusst gezogene Grenze**: Aggregation, Joins und
Reporting sind „bewusst nicht im Kern" (README); abgeleitete, materialisierte
**Read Models** baut man außerhalb (CQRS) — so entschieden für die Sekundär-Query
(ADR-029, externes Read-Model zurückgestellt) und für die globale Total-Order
(ADR-036, Order-Read-Modell ist nachgelagert, kein Store-Feature). Diese ADR muss
den neuen Komfort liefern, **ohne** diese Grenze zu verletzen.

Die Spannung: clio ist ein **generischer** Event Store und kennt die
domänenspezifische Reduce-/Fold-Funktion einer Entität nicht. Ein „gib mir den
Zustand"-Endpoint ist nur wohldefiniert, wenn er eine **explizite, dokumentierte
Falt-Konvention** festlegt — statt eine Reduktion zu erfinden, die der Client nicht
kontrolliert.

**Entscheidung**

1. **Neue Komfort-Leseroute `GET /api/v1/state/<subject>`** (Scope `read`,
   subject-berechtigt nach ADR-033). Sie faltet die Events **eines** Subjects zu
   einer aktuellen Zustandssicht und gibt **ein JSON-Objekt** zurück (kein NDJSON):
   `{subject, state, revision, eventCount, firstEventId, lastEventId,
   lastEventType, lastEventTime, at?}`.

2. **Falt-Konvention: Last-Write-Wins-Deep-Merge der `data`-Payloads** in
   Schreibreihenfolge (älteste → jüngste):
   - Objekte werden **rekursiv pro Schlüssel** verschmolzen.
   - Skalare, Arrays und Typwechsel **ersetzen** den bisherigen Wert vollständig.
   - JSON `null` als Wert ist ein **Tombstone**: der Schlüssel wird gelöscht (so
     kann ein späteres Event ein Feld bewusst zurücknehmen).
   - `data`, das leer oder **kein** JSON-Objekt ist (Array/Skalar), trägt nichts zur
     Feld-Sicht bei; das Event zählt aber weiter und bewegt die Metadaten.

   Diese Konvention ist **dokumentiert und stabil**, nicht konfigurierbar — sie ist
   der Vertrag. Wer eine andere Reduktion braucht, baut sie als Read-Model extern.

3. **Bewusst single-subject, nicht rekursiv.** Ein Subject = ein Aggregat = ein
   Stream (ADR-005). Damit ist der Read **single-partition** und so günstig wie ein
   normaler Subject-Read (ADR-034/036: single-source bleibt single-partition). Eine
   Teilbaum-Aggregation über mehrere Subjects ist eine andere, mehrdeutige Frage und
   bleibt **außen** (ADR-029/036).

4. **Zeitreise über `?at=<eventId>`** (inklusive obere Event-ID-Grenze): die Sicht
   wird „as of" diesem Revisionsstand rekonstruiert. Das macht den Kernvorteil des
   Event-Sourcings — jeden Stand nachfahren zu können — direkt an der API nutzbar.
   Optionaler `?type=`-Filter (wiederholbar) faltet nur ausgewählte Typen.

5. **404, wenn das Subject (im gewählten `at`/`type`-Fenster) keine Events hat** —
   sauberer Unterschied zwischen „leeres Objekt" und „nicht vorhanden".

6. **Kein materialisiertes Read-Model.** Der Zustand wird **bei jedem Aufruf frisch
   gefaltet** (streamender Subject-Read, konstanter Speicher), nichts wird
   persistiert. Damit ist dies eine reine **Lese-Komfortschicht** über dem Store —
   und kein Widerspruch zu ADR-029/036, die externe/persistierte Read-Models meinen.

**Konsequenzen**

*Positiv (gewonnen):*

- Der häufigste Lesebedarf — „aktueller Stand einer Entität" — ist **ein
  GET-Aufruf**, ohne dass der Client die Historie selbst faltet.
- Zeitreise (`at`) und Typ-Filter machen Nachvollziehbarkeit und partielle Sichten
  trivial nutzbar, gestützt auf die bestehenden Index-/Bounds-Mechanismen.
- Single-subject hält den Pfad **partitionierungs-freundlich** (ADR-034/036) und so
  günstig wie ein normaler Subject-Read — der Zustand wird streamend gefaltet
  (konstanter Server-Speicher, unabhängig von der Event-Zahl des Subjects).
- Die Kerngrenze bleibt gewahrt: keine Joins, keine Cross-Subject-Aggregation, kein
  persistiertes Read-Model.

*Negativ / Grenzen (geopfert):*

- **Eine einzige, feste Falt-Semantik** (LWW-Deep-Merge mit null-Tombstone) passt
  nicht auf jede Domäne (z. B. Zähler/Summen, Listen-Append, Conflict-Resolution).
  Solche Reduktionen bleiben Sache eines externen Read-Models (CQRS). Die Konvention
  ist bewusst einfach und vorhersehbar statt mächtig.
- **Jeder Aufruf faltet neu** (keine Cache-/Snapshot-Schicht). Für sehr lange
  Streams eines einzelnen Subjects ist das O(Events des Subjects) je Anfrage — für
  den typischen Aggregat-Stream unkritisch, für extreme Fälle ggf. später ein
  optionaler Snapshot (Folge-ADR).
- **Recursive/Teilbaum-Zustand** wird nicht beantwortet — bewusst, wegen
  Mehrdeutigkeit der Merge-Semantik über mehrere Subjects.

**Offene Punkte / Folge-ADRs**

- **Snapshot-/Caching-Schicht** für sehr lange Einzel-Streams (Fold-Memoisierung
  ab Revision N) — erst bauen, wenn ein konkreter Stream das rechtfertigt.
- **Wählbare/registrierbare Reduktionen** (z. B. CEL-basierter Reducer, additive
  Felder) — nur falls ein realer Bedarf über LWW-Deep-Merge hinausgeht; sonst bleibt
  das die Domäne eines externen Read-Models.
- **Verhalten unter Partitionierung** für den (hier ausgeschlossenen) rekursiven
  Fall — verbunden mit dem zurückgestellten Order-Read-Modell aus ADR-036.

**Referenzen**

- ADR-005 (Subjects als hierarchische Stream-Identifier) — ein Subject = ein
  Aggregat/Stream, Grundlage der single-subject-Faltung.
- ADR-029 (Sekundär-Query, externes Read-Model zurückgestellt) und ADR-036
  (Read-Path & CQRS, Order-Read-Modell nachgelagert) — diese ADR respektiert deren
  Grenze: sie liefert eine **nicht-persistierte Lese-Komfortschicht**, kein
  materialisiertes Read-Model.
- ADR-010 (Komfort-Leseroute `GET /events/<subject>`) — Vorbild für die
  Pfad-basierte Route und Subject-aus-Pfad-Bildung.
- ADR-033 (Subject-/Prefix-Scopes) — die Route ist exakt-subject-berechtigt.
