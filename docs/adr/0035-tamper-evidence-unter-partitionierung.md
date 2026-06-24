# ADR-035: Tamper-Evidence unter Partitionierung (n Ketten + globaler Anker)

> **Kontext-Cluster:** Verteiltes System / Skalierung. Folge-ADR zu
> [ADR-034](./0034-partitionierungsmodell-fuer-horizontale-skalierung.md) — sie
> löst den dort offen gelassenen Punkt „übergeordnetes Anchoring der n Hash-Chains".
> Entscheidet **ausschließlich** die Tamper-Evidence-Mechanik unter Partitionierung;
> Storage-Engine, Read-Path/CQRS und Distribution/Consensus bleiben eigenen ADRs.

**Status:** Vorgeschlagen

**Datum:** 2026-06-24

**Kontext**

Heute hat Clio **eine** Hash-Kette (ADR-012): der Store vergibt über *einen*
`events`-Bucket eine global monotone Sequenz (`NextSequence`), die Event-ID **ist**
diese Sequenz, jedes Event trägt den `PredecessorHash` des globalen Heads, und
`verifyChain` rechnet **eine** lineare Kette von `GenesisHash` aus nach (verifiziert
im Konsumenten-Audit, [`docs/plans/partitioning-consumer-audit.md`](../plans/partitioning-consumer-audit.md) §1).

ADR-034 ersetzt das durch **n Partitionen mit je eigener Kette und Sequenz**. Damit
zerfällt die *eine* Tamper-Evidence-Garantie in **n** unabhängige Garantien. Das
allein ist unzureichend: Mit rein per-Partition-Ketten wäre ein **Angriff auf der
Partitionsebene unsichtbar** —

- **Drop einer ganzen Partition:** Eine vollständige Partition (Datei/Bucket) wird
  entfernt. Jede verbleibende Kette verifiziert weiter fehlerfrei; nichts beweist,
  dass die Partition je existierte.
- **Rollback einer Partition:** Eine Partition wird auf einen früheren, in sich
  konsistenten Zustand zurückgesetzt. Ihre eigene Kette ist intakt; kein anderer
  Teil des Systems widerspricht.

Eine einzelne intakte Kette beweist nur die Integrität **innerhalb** ihrer Partition.
Es fehlt ein Mechanismus, der die **Menge** der Partitionen als Ganzes bindet — ohne
dafür die globale Schreib-Serialisierung wieder einzuführen, die ADR-034 gerade
abgeschafft hat.

Zusätzlich offen (aus ADR-034): Wie wird der **Bestand** — die existierende *eine*
Kette — überführt, ohne ADR-012 (Tamper-Evidence) und ADR-015 (Append-only, es wird
nichts umgeschrieben) zu verletzen?

**Entscheidung**

1. **Pro Partition eine unabhängige Kette.** Jede Partition führt ihre eigene Kette
   von einem **partitionseigenen Genesis** (`GenesisHash ∥ partitionID`) und ihre
   eigene per-Partition-Sequenz. `verifyChain` läuft unverändert, aber **pro
   Partition**. Es gibt **kein** Cross-Chaining zwischen Partitionen (ein Event
   verweist nie auf den Head einer fremden Partition) — sonst entstünde wieder eine
   globale Schreib-Ordnung und damit das Nadelöhr aus ADR-034.

2. **Globaler Anker über einen Merkle-Commitment der Partitions-Heads.** In
   konfigurierbarer Kadenz (zeit- **oder** ereignisgetrieben) bildet ein
   **Anker-Koordinator** einen Merkle-Baum über die aktuellen Tupel
   `(partitionID, head, seq)` **aller** Partitionen und schreibt die **Merkle-Wurzel**
   als **Anker-Datensatz** in ein eigenes, append-only Anker-Log
   (`(anchorSeq, prevAnchorHash, merkleRoot, perPartition[{id, head, seq}], time)`,
   selbst eine Hash-Kette). Der Anker ist die periodische, fälschungssichere Aussage
   „**zum Zeitpunkt t bestanden genau diese n Partitionen mit genau diesen Heads**".

3. **Der Anker-Koordinator liest nur, er serialisiert keine Writes.** Er nimmt je
   Partition einen Read-Snapshot des Heads (analog zum konsistenten Backup-Read,
   ADR-031) — er hält **keinen** partitionsübergreifenden Schreib-Lock und blockiert
   die n Writer nicht. Damit bleibt Single-Writer-**per-Partition** (ADR-034)
   unangetastet; die globale Bindung ist ein **nachgelagerter, read-only**
   Vorgang, kein Schreibpfad-Consensus.

4. **Globaler Verify = Konjunktion + Anker.** `verify` prüft (a) **jede**
   Partitionskette einzeln von ihrem Genesis (wie heute, n-fach) **und** (b) dass die
   beobachteten Partitions-Heads den **letzten Anker** reproduzieren (Merkle-Wurzel
   stimmt) **und** (c) dass die Anker-Kette selbst lückenlos ist. Schlägt (b) fehl
   oder fehlt eine im Anker enthaltene Partition → **Manipulation erkannt**, auch
   wenn jede Einzelkette für sich intakt ist. Das schließt Drop/Rollback ganzer
   Partitionen **bis zur letzten Ankergranularität**.

5. **Migration über eine versiegelte Epoche-0, kein Re-Chaining.** Die bestehende
   *eine* Kette wird **nicht** in n Ketten umgeschrieben (das verletzte ADR-015). Sie
   wird als **Epoche-0** unverändert versiegelt: ihr finaler Head wird als
   **Genesis-Anker** (`anchorSeq = 0`) festgehalten. Partitionierung gilt ab
   **Epoche-1**; jede Partition startet ihre Kette frisch von ihrem partitionseigenen
   Genesis, und der erste reguläre Anker bindet `{Epoche-0-Head, Partition-Genesis-
   Heads}`. Die alte Kette bleibt als Ganzes mit dem bestehenden `verifyChain`
   prüfbar; Geschichte wird weder bewegt noch neu gehasht.

**Konsequenzen**

*Positiv (gewonnen):*

- Tamper-Evidence über den **gesamten** Store ist wiederhergestellt: Drop oder
  Rollback einer ganzen Partition wird beim Verify gegen den Anker erkannt — ohne die
  globale Schreib-Serialisierung aus ADR-034 zurückzuholen.
- Single-Writer-per-Partition und horizontaler Schreibdurchsatz bleiben erhalten; die
  globale Bindung ist read-only und liegt **außerhalb** des Schreibpfads.
- Die Bestands-Migration ist sauber: **nichts** an der Historie wird umgeschrieben
  (ADR-015 gewahrt), die alte Kette bleibt unverändert verifizierbar (ADR-012).
- Der Anker ist ein kompaktes, exportierbares Integritäts-Artefakt (Merkle-Wurzel +
  Heads) — eignet sich später für externe Notarisierung/Zeugen.

*Negativ / Grenzen (geopfert):*

- **Cross-Partition-Tamper-Evidence ist anker-granular, nicht event-granular.**
  Zwischen zwei Ankern ist ein partitionsweiter Rollback auf einen *anker-konsistenten*
  Zwischenstand nur beim **nächsten** Anker erkennbar. Die Kadenz ist ein bewusster
  Trade-off (Stärke der Garantie ↔ Anker-Last). Innerhalb einer Partition bleibt die
  Evidenz event-granular (Kette).
- Der Anker-Koordinator ist eine **neue Komponente** mit eigener Lebenszeit/Konfig.
  In einem späteren verteilten Setup (ADR „Distribution/Consensus") braucht er eine
  konsistente Sicht auf alle Partitions-Heads — das ist ein zu lösender Punkt, aber
  **read-only** und damit deutlich schwächer als Schreib-Consensus.
- IDs sind nicht mehr global dicht/vergleichbar (Folge aus ADR-034, im Audit als
  Breaking Change am Cursor-Vertrag dokumentiert); das ist nicht Gegenstand dieser
  ADR, aber Voraussetzung dafür, dass per-Partition-Ketten überhaupt tragen.

**Invariante (neu)**

> **INV-P2:** Jede Partition besitzt eine lückenlose, von ihrem partitionseigenen
> Genesis verifizierbare Hash-Kette (verfeinert INV-P1). Die Menge aller Partitionen
> wird zusätzlich durch eine **append-only Anker-Kette** gebunden, deren jeweils
> letzter Anker einen Merkle-Commitment über `(partitionID, head, seq)` **aller**
> Partitionen festhält. Ein gültiger globaler Verify verlangt: jede Partitionskette
> intakt **und** die aktuellen Heads reproduzieren den letzten Anker **und** die
> Anker-Kette ist lückenlos. Cross-Partition-Manipulation ist damit **bis zur letzten
> Ankergranularität** erkennbar.

**Umsetzung**

Der „Wie"-Teil (Anker-Koordinator, Kadenz-Konfig, Verify-Erweiterung,
Epoche-0-Versiegelung) wird im Umsetzungsplan
[`docs/plans/partitioning-plan.md`](../plans/partitioning-plan.md) als zusätzliches
Work-Package geführt und ist gegen WP-2 (Per-Partition-Ketten) gated.

**Offene Punkte / Folge-ADRs**

- **Anker-Kadenz** (Default zeit- vs. ereignisgetrieben, konkrete Werte) — bewusst
  als Konfig offen gelassen; ein vernünftiger Default ist im Plan zu bestimmen.
- **Verteilte Anker-Erzeugung:** Wer koordiniert den Anker, wenn Partitionen über
  mehrere Knoten verteilt sind, und wie wird die konsistente Head-Sicht eingeholt?
  → ADR „Distribution / Consensus" (read-only Snapshot-Sicht, kein Schreib-Consensus).
- **Externe Notarisierung:** Ob/wie die Anker-Wurzel an einen externen Zeugen
  (Transparency-Log, ggf. Ed25519-signiert wie ADR-016) gebunden wird — späteres,
  additives ADR.
- **Sub-Partitionierung heißer Keys** (aus ADR-034): wie Splits in den Merkle-Baum
  eingehen, ohne ältere Anker zu invalidieren.

**Referenzen**

- ADR-034 (Partitionierungsmodell) — diese ADR löst dessen offenen Anchoring-Punkt
  und verfeinert INV-P1 → INV-P2.
- ADR-012 (Hash-Kette / Tamper-Evidence) — pro Partition unverändert angewandt.
- ADR-015 (Kompaktierung löscht keine Events / Append-only) — durch die
  Epoche-0-Versiegelung statt Re-Chaining gewahrt.
- ADR-031 (konsistenter Read-Snapshot für Backup) — Vorbild für die read-only
  Head-Sicht des Anker-Koordinators.
- ADR-016 (Ed25519-Signaturen) — möglicher Baustein für externe Anker-Notarisierung.
- Konsumenten-Audit [`docs/plans/partitioning-consumer-audit.md`](../plans/partitioning-consumer-audit.md) — §1 (eine Kette heute), §6 (Eingabe für diese ADR).
