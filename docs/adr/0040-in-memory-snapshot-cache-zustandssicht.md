# ADR-040: In-Memory-Snapshot-Cache für die Zustandssicht (lazy-inkrementelle Fold-Memoisierung)

**Status:** Akzeptiert (2026-06-26)

**Datum:** 2026-06-26

**Kontext**

ADR-039 liefert mit `GET /state/<subject>` eine gefaltete Zustandssicht eines
Subjects und nimmt dort bewusst in Kauf, dass **jeder Aufruf den Stream des
Subjects neu faltet** (O(Events des Subjects) je Anfrage). Für ein heiß gelesenes
Aggregat mit langer Historie ist das verschwenderisch: zwischen zwei Reads ändern
sich meist **wenige oder keine** Events, trotzdem wird der gesamte Stream erneut
durchlaufen. ADR-039 hat eine Snapshot-/Caching-Schicht als Folge-ADR offen
gelassen.

Kräfte: Der Cache muss (a) **korrekt** bleiben, wenn neue Events committen oder
eine Reduce-Spec (ADR-041) sich ändert; (b) den Kern **abhängigkeitsarm** halten
(ADR-001) und **nicht in den Write-Path eingreifen**; (c) den Speicher beschränken;
(d) die Append-only-Garantie ausnutzen (ADR-006/015: bereits geschriebene Events
sind unveränderlich, Kompaktierung löscht nichts).

**Entscheidung**

1. **Ephemerer In-Memory-LRU-Cache** gefalteter Subject-Zustände im HTTP-Layer,
   size-bound (Default 2048 Subjects, LRU-Verdrängung). Beim Prozessstart leer und
   nicht persistiert — ein **Read-seitiger** Beschleuniger, kein Speicher der
   Wahrheit. (Persistente Snapshots wurden verworfen: sie werfen Write-Path-,
   Konsistenz- und Tamper-Evidence-Fragen auf, ohne die der Append-only-Kern
   einfach bleibt.)

2. **Lazy-inkrementelle Fortschreibung („pull", kein „push").** Der Cache hängt
   **nicht** am Broker/Write-Path. Bei einem Request wird der gecachte Stand ab der
   zuletzt eingefalteten Event-ID **weitergefaltet**: ein Subject-Read mit
   `LowerBound = lastSeq+1` liefert nur die Differenz (meist 0 Events → fast leerer
   Indexscan). Das ist korrekt, weil Events append-only und unveränderlich sind
   (ADR-006/015): bereits eingefaltete Events ändern sich nie.

3. **Fingerprint bindet den Stand an die Reduce-Spec (ADR-041).** Der Cache-Eintrag
   trägt den kanonischen Inhalt der wirksamen Reduce-Spec als Fingerprint. Ändert
   sich die Spec, passt der Fingerprint nicht mehr → vollständige Neufaltung. So
   invalidiert eine Spec-Änderung implizit und ohne zusätzliche Kopplung.

4. **Nur die „nackte" Abfrage wird gecacht.** `at=` (Zeitreise) und `type=`-Filter
   (ADR-039) umgehen den Cache und falten frisch — sie sind selten und vielfältig;
   sie zu cachen brächte wenig und verkomplizierte die Invalidierung.

5. **Auslieferung als Kopie.** Da gecachte Zustände unveränderlich sind, sobald sie
   abgelegt wurden, gibt der Cache für die Antwort eine tiefe Kopie heraus; ein
   inkrementeller Folge-Read kopiert die Basis, bevor er die Differenz einfaltet.
   Damit teilt keine Antwort eine veränderliche Map mit dem Cache.

6. **Explizites Leeren beim Dev-Reset (ADR-022).** Nach einem Reset beginnt die
   Sequenz wieder bei 1; gecachte Stände mit höherer `lastSeq` würden neue Events
   sonst nicht inkrementell aufnehmen. `handleDevReset` leert daher den Cache. Der
   Online-Reopen (Compaction/Grow, ADR-015) lässt IDs unverändert und braucht kein
   Leeren.

**Konsequenzen**

*Positiv (gewonnen):*

- Wiederholte Reads eines Aggregats kosten nach dem ersten nur noch das Einfalten
  der **Differenz** — bei unveränderten Subjects nahe O(1).
- Keine Write-Path-Kopplung, kein Hintergrund-Goroutine: der Cache ist ein reiner
  Lese-Beschleuniger, der die Append-only-Semantik ausnutzt.
- Spec-Änderungen invalidieren über den Fingerprint automatisch; keine separate
  Invalidierungslogik.
- Speicher ist durch LRU hart begrenzt.

*Negativ / Grenzen (geopfert):*

- **Kein Kaltstart-Vorteil:** nach (Neu-)Start ist der Cache leer; der erste Read je
  Subject faltet voll. Bewusst — persistente Snapshots wären der Preis dafür.
- **Pro Instanz, nicht geteilt:** unter mehreren Instanzen hat jede ihren eigenen
  Cache (konsistent, da derselbe deterministische Fold; nur redundant gefüllt).
- `at`/`type`-Abfragen profitieren nicht.
- Der inkrementelle Read setzt **strikte ID-Monotonie je Subject** voraus — unter
  Partitionierung (ADR-034) gilt das innerhalb einer Partition; ein Subject liegt
  per Konstruktion in einer Partition, der single-subject-Fold bleibt also gültig.

**Offene Punkte / Folge-ADRs**

- **Persistente/geteilte Snapshots** (z. B. eigener Bucket oder externer Cache) —
  erst, wenn Kaltstartkosten oder Instanz-Redundanz das rechtfertigen.
- **Cache-Metriken** (Hit/Miss, Größe) für das Dashboard — klein, später.
- **Konfigurierbare Cache-Größe** (statt Konstante) — bei Bedarf.

**Referenzen**

- ADR-039 (Zustandssicht über REST) — diese ADR löst deren offenen Punkt
  „Snapshot-/Caching-Schicht".
- ADR-041 (Reduce-Specs) — liefert den Fingerprint, der den Cache an die wirksame
  Faltungsstrategie bindet.
- ADR-006/015 (Append-only, Kompaktierung löscht nicht) — Grundlage der Korrektheit
  der inkrementellen Fortschreibung.
- ADR-022 (Dev-Reset) — Auslöser für das explizite Cache-Leeren.
