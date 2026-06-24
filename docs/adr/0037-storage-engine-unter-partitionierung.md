# ADR-037: Storage-Engine unter Partitionierung (bbolt-Datei pro Partition)

> **Kontext-Cluster:** Verteiltes System / Skalierung. Folge-ADR zu
> [ADR-034](./0034-partitionierungsmodell-fuer-horizontale-skalierung.md) — sie löst
> den dort offen gelassenen Punkt „Storage-Engine jenseits der heutigen
> bbolt-Single-File-Ablage". Entscheidet **ausschließlich** die physische Engine
> unter Partitionierung; Tamper-Evidence (ADR-035) und Read-Path (ADR-036) sind
> separat entschieden.

**Status:** Vorgeschlagen

**Datum:** 2026-06-24

**Kontext**

Heute liegt der gesamte Store in **einer** bbolt-Datei (ADR-006), mit einer
1:1-Abbildung Event-Strom → Datei. Der `storage-scaling-plan.md` hat die Grenzen
dieser Single-File-Ablage **vermessen** (Benchmark 2026-06-18): ab ~85 % Füllgrad
bricht der Durchsatz ein, bei ~95 % praktischer Stillstand. Wurzeln (per
Code-Verifikation, bbolt v1.4.3):

- **Mmap-Remap** in `db.allocate()`: überschreitet der Highwater-Mark die
  Mmap-Grenze, ruft bbolt `munmap+mmap`, nimmt `mmaplock` exklusiv und **wartet auf
  alle Lese-Transaktionen** → Schreib-Latenzspitzen unter Leselast.
- **B+Tree-Rebalancing/Page-Splits** bei tiefem Tree und hohem Füllgrad.
- Faktische Bindung der aktiven Pages an **Adressraum/RAM** einer einzelnen Datei.

ADR-034 partitioniert den Schreibpfad (n Writer, n Ketten, n Sequenzen). Damit
stellt sich die Engine-Frage neu: Bleibt bbolt — und wenn ja, in welcher Ablage —
oder rechtfertigt die 10⁹+-Zielgröße einen Wechsel auf eine andere Engine (z. B. eine
LSM-Tree-Engine, die sequenzielle Append-Lasten ohne In-Place-Rebalancing aufnimmt)?

Die Entscheidung steht im Spannungsfeld der Projektprinzipien: **Abhängigkeitsarmut
und pure-Go/Single-Binary** (ADR-001), **Append-only** (ADR-006/015) und die bereits
erprobte Toolchain (Verify ADR-012, Backup/Restore ADR-031, Kompaktierung ADR-015,
Group Commit ADR-009).

**Entscheidung**

1. **bbolt bleibt die Engine — aber eine eigene bbolt-Datei pro Partition**
   („file-per-partition"). **Kein** Engine-Wechsel. Die heutige 1:1-Abbildung
   Strom→Datei wird zu 1:1 **Partition→Datei**.

2. **Die Single-File-Limits werden durch Partitionierung strukturell entschärft, nicht
   nur getunt.** Jede Partitionsdatei ist kleiner → **flacherer** B+Tree, **kleinere**
   Mmap, **weniger** Remap-Druck, und die n Writer kontendieren **nicht** mehr um
   *einen* Datei-Lock (bbolt hält pro Datei einen exklusiven Schreib-Lock — der ist
   nun pro Partition). Das adressiert exakt die im `storage-scaling-plan` vermessenen
   Ursachen an der Wurzel.

3. **Die erprobte Toolchain gilt unverändert, pro Partition.** Single-Writer +
   Group Commit (ADR-003/009) = ein Writer pro Datei; Verify (ADR-012/035),
   Backup/Restore (ADR-031) und Online-Kompaktierung (ADR-015) laufen je Datei. Die
   Tuning-Hebel aus dem `storage-scaling-plan` (`InitialMmapSize`-Vorab-Mmap,
   Headroom-Monitor, Reopen-basierte Online-Compaction) wirken **pro Partition** und
   bleiben gültig.

4. **Offene Datei-/Mmap-Handles werden begrenzt.** n Dateien × Mmap kosten Adressraum
   und File-Descriptors; bei vielen Partitionen ist „alle offen halten" nicht tragbar.
   Daher ein **Handle-Pool mit lazy open/close (LRU)**: nur die aktiv beschriebenen/
   gelesenen Partitionen sind gemappt; selten genutzte werden geschlossen und bei
   Bedarf reattacht. Die Pool-Größe ist konfigurierbar.

5. **Die Engine sitzt hinter der Partitions-Abstraktion und ist damit lokal
   austauschbar.** Weil jede Partition ein in sich geschlossener Store ist, ist ein
   **späterer** Engine-Wechsel (z. B. eine LSM-Engine für besonders heiße/große
   Partitionen) eine **per-Partition-Implementierungsfrage** hinter derselben
   Schnittstelle — kein globaler Big-Bang. Ein solcher Wechsel wird **jetzt bewusst
   verworfen** (pure-Go-bbolt erfüllt die Anforderungen, sobald die Datei beschränkt
   ist; ein neuer, schwerer Storage-Dependency widerspräche ADR-001) und erst dann
   wieder erwogen, wenn eine **einzelne** Partition trotz Beschränkung an bbolt-Grenzen
   stößt.

**Konsequenzen**

*Positiv (gewonnen):*

- Die Durchsatz-Klippe des Single-File-Modells wird **strukturell** beseitigt
  (bounded Datei + kein gemeinsamer Schreib-Lock), nicht nur hinausgeschoben.
- **Keine neue Abhängigkeit**, pure-Go, Single-Binary (ADR-001) bleibt; die gesamte
  bewährte bbolt-Toolchain (Verify/Backup/Compact/Group-Commit) wird wiederverwendet
  statt neu gebaut.
- Backup/Restore wird natürlich **partitionsgranular** (ADR-031 pro Datei) — feinere
  DR-Einheiten.
- Die Engine ist gekapselt: ein künftiger, gezielter LSM-Einsatz für einzelne
  Partitionen ist möglich, ohne den Rest anzufassen.

*Negativ / Grenzen (geopfert):*

- **Handle-/Mmap-Management wird zur echten Aufgabe.** Lazy open/close (LRU) bringt
  Reopen-Latenz für „kalte" Partitionen und neuen Zustand (Pool) in den Betrieb.
- **Viele kleine Dateien** statt einer großen: mehr FDs, mehr Backup-Artefakte, ein
  konsistenter **Store-weiter** Snapshot ist nicht mehr ein einzelnes `Tx.WriteTo`,
  sondern eine koordinierte Menge pro-Partition-Snapshots (passt aber zur
  read-only Anker-Sicht aus ADR-035).
- **bbolt bleibt ein B+Tree**, kein Append-optimierter LSM. Für eine *einzelne*
  extrem große/heiße Partition kehren die bekannten Grenzen zurück — dann greift die
  Sub-Partitionierung (ADR-034-Folge) bzw. der gekapselte Engine-Wechsel (Punkt 5).
- Pro-Partition-Vorab-Mmap (`InitialMmapSize`) × n Partitionen muss budgetiert werden,
  sonst summiert sich vorbelegter Adressraum.

**Invariante**

Diese ADR führt **keine** neue Invariante ein: Die physische Engine ist ein
**Mechanismus hinter** INV-P1 (Partition = eigener Writer/Kette/Sequenz, ADR-034),
INV-P2 (Tamper-Evidence, ADR-035) und INV-P3 (Lesepfad, ADR-036), keine eigene
semantische Garantie. „bbolt-Datei pro Partition" ist eine Umsetzungswahl, die diese
Invarianten trägt, sie aber nicht erweitert.

**Umsetzung**

Die Storage-Substrat-Entscheidung ist die Grundlage von **WP-2** (Per-Partition-
Writer & -Kette) im Umsetzungsplan
[`docs/plans/partitioning-plan.md`](../plans/partitioning-plan.md): „eigener
Bucket-Namespace pro Partition" wird zu „eigene **Datei** pro Partition". Der
**Handle-Pool (lazy open/close, LRU)** kommt als Akzeptanzpunkt zu WP-2 hinzu. Die
per-Partition-Anwendung der `storage-scaling-plan`-Hebel wird dort vermerkt.

**Offene Punkte / Folge-ADRs**

- **Pool-Dimensionierung & Reopen-Strategie:** Default-Größe, Verdrängungspolitik,
  Verhalten bei Lese-/Schreib-Spitzen über viele kalte Partitionen.
- **Store-weiter konsistenter Snapshot** über n Dateien (Koordination mit der
  Anker-Sicht aus ADR-035; PITR-Stufe 2 aus `backup-restore-dr-concept.md`).
- **Kriterien für den gekapselten Engine-Wechsel** einzelner Partitionen (ab welcher
  Partitionsgröße/-last lohnt LSM?) — erst bei Bedarf, mit Benchmark-Beleg wie im
  `storage-scaling-plan`.
- **Wechselwirkung mit Sub-Partitionierung heißer Keys** (ADR-034-Folge): Split einer
  Datei in mehrere.

**Referenzen**

- ADR-034 (Partitionierungsmodell) — diese ADR liefert dessen Storage-Substrat;
  keine neue Invariante (Mechanismus hinter INV-P1/P2/P3).
- ADR-006 (Append-only Storage / bbolt) — bleibt gültig, nun 1:1 Partition→Datei.
- ADR-001 (Abhängigkeitsarmut, pure-Go) — tragender Grund gegen einen Engine-Wechsel.
- ADR-003/009 (Single-Writer / Group Commit) — ein Writer pro Datei.
- ADR-012/035 (Verify/Tamper-Evidence), ADR-015 (Kompaktierung), ADR-031
  (Backup/Restore) — Toolchain pro Partition.
- `docs/plans/storage-scaling-plan.md` — die vermessenen Single-File-Limits
  (Mmap-Remap, Rebalancing) und die Tuning-Hebel, die nun pro Partition gelten.
