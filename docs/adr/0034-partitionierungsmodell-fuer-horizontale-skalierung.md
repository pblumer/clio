# ADR-034: Partitionierungsmodell für horizontale Skalierung

> **Kontext-Cluster:** Verteiltes System / Skalierung. Diese ADR entscheidet
> ausschließlich über das **Partitionierungsmodell** als Fundament. Storage-Engine,
> Tamper-Evidence-Mechanik, Read-Path und Consensus werden in den unten genannten
> Folge-ADRs entschieden und hier nur verlinkt, nicht ausgebreitet.

**Status:** Vorgeschlagen

**Datum:** 2026-06-24

**Kontext**

Clio ist derzeit ein Single-Writer-Store mit einer globalen, strikt sequenziellen
Hash-Chain über alle Events (serialisierte Schreibvorgänge ADR-003, Hash-Kette
ADR-012, Single-Instance ADR-002). Diese Eigenschaft liefert globale Total-Order
und Tamper-Evidence über genau **eine** Kette.

Bei einer Eventmenge in der Größenordnung von 10⁹+ bricht dieses Modell an mehreren
Stellen:

- **Single-Writer als Nadelöhr:** Die globale Hash-Chain erzwingt genau einen
  Writer für den gesamten Store (ADR-003). Schreibdurchsatz ist nicht horizontal
  skalierbar.
- **bbolt-Grenzen:** Single-File-B+Tree mit mmap (Append-only-Ablage ADR-006);
  tiefer Tree, teure Page-Splits, faktische Bindung der aktiven Pages an
  Adressraum/RAM.
- **Query-Path:** CEL-Full-Scans (ADR-017) und Memory-Materialisierung in
  `read-events` sind bei dieser Größe unbrauchbar bzw. OOM-gefährdet.

Diese ADR entscheidet ausschließlich über das **Partitionierungsmodell** als
Fundament. Die Storage-Engine, die Tamper-Evidence-Mechanik unter Partitionierung,
der Read-Path und der Verteilungs-/Consensus-Mechanismus werden in den referenzierten
Folge-ADRs entschieden (siehe „Offene Punkte / Folge-ADRs").

**Entscheidung**

Clio wird entlang einer **Partitionsachse** partitioniert. Jede Partition besitzt:

1. einen **eigenen Writer** (Single-Writer-per-Partition),
2. eine **eigene, unabhängige Hash-Chain**,
3. eine **eigene Sequenznummer** (per-Partition monoton steigend).

*Partitionsachse.* Die Partitionszugehörigkeit wird aus dem CloudEvents-`source`
(ADR-004) abgeleitet (bzw. einem daraus bestimmten Stream-/Aggregate-Key). `source`
ist die fachlich tragende Achse, weil Events desselben Aggregats fast immer gemeinsam
gelesen und kausal geordnet werden müssen (vgl. Subjects als hierarchische
Stream-Identifier, ADR-005). Das Mapping `source → partition` erfolgt über
**konsistentes Hashing**, um Rebalancing bei Knotenänderung mit minimaler
Key-Migration zu erlauben (Details in der Folge-ADR zu Distribution/Consensus).

*Ordnungsgarantie.*

- **Innerhalb einer Partition:** strikte Total-Order erhalten (Sequenz + Hash-Chain).
- **Über Partitionen hinweg:** **keine** globale Total-Order mehr. Nur partielle
  Ordnung; globale Reihenfolge ist über Wall-Clock / Hybrid-Logical-Clock allenfalls
  *approximierbar*, aber **keine** Garantie.

**Konsequenzen**

*Positiv (gewonnen):*

- Horizontal skalierbarer Schreibdurchsatz (n Writer statt 1).
- Schreib- und Speicherlast pro Partition begrenzbar.
- Single-Writer-per-Partition ist ein in diesem Ökosystem bereits erprobtes Modell
  (vgl. Chrampfer: Single-Writer-per-Partition mit Group Commit, ADR-009).

*Negativ / Grenzen (geopfert):*

- **Globale Total-Order entfällt.** Dies ist die folgenschwerste Einzelentscheidung
  dieses Clusters und weicht bewusst von ADR-002/ADR-003 ab. Konsumenten, die auf
  eine einzige globale Sequenz angewiesen sind, müssen umgestellt werden (z. B. auf
  per-Partition-Cursor oder eine nachgelagerte Order-Projektion).
- Tamper-Evidence verschiebt sich von *einer* Chain (ADR-012) auf *n* Chains und
  braucht ein übergeordnetes Anchoring → Folge-ADR „Tamper-Evidence unter
  Partitionierung".
- Cross-Partition-Queries und -Transaktionen werden teurer / brauchen ein Read-Modell
  → Folge-ADR „Read-Path / CQRS".

*Risiken:*

- **Hot Partitions:** Ungleichverteilte `source`-Last kann einzelne Partitionen
  überlasten. Mitigation: Sub-Partitionierung heißer Keys, in einer Folge-ADR zu
  behandeln.
- **Reihenfolge-Erwartungen im Bestand:** Bestehende Clients könnten implizit globale
  Ordnung annehmen. Ein Audit der Konsumenten ist nötig.

**Invariante (neu)**

> **INV-P1:** Innerhalb einer Partition gilt strikte Total-Order und eine lückenlose,
> verifizierbare Hash-Chain. Über Partitionsgrenzen hinweg existiert **keine**
> globale Total-Order — nur partielle Ordnung. Jeder Konsument, der globale Ordnung
> benötigt, muss diese explizit über ein nachgelagertes Read-Modell rekonstruieren.

**Umsetzung**

Der „Wie"-Teil (Work-Packages, Akzeptanzkriterien, Etappen, Migration) steht im
Umsetzungsplan [`docs/plans/partitioning-plan.md`](../plans/partitioning-plan.md)
und wird hier nur verlinkt, nicht ausgebreitet.

**Offene Punkte / Folge-ADRs**

Diese ADR legt nur das Fundament. Bewusst offen und je einer eigenen Folge-ADR
vorbehalten:

- **Anzahl/Granularität der Partitionen** (fix vs. dynamisch) sowie
  **Splitting/Merging** von Partitionen zur Laufzeit.
- **Exaktes `source → partition`-Mapping und Rebalancing-Strategie**
  → Folge-ADR „Distribution / Consensus".
- **Verhalten bei Events ohne eindeutigen `source`** (vgl. tokenlose Writes /
  Inbox-Stream, ADR-026).
- **Übergeordnetes Anchoring der n Hash-Chains** → entschieden in
  [ADR-035](./0035-tamper-evidence-unter-partitionierung.md) (n Ketten + globaler
  Merkle-Anker, baut auf ADR-012 auf).
- **Storage-Engine** jenseits der heutigen bbolt-Single-File-Ablage (ADR-006)
  → entschieden in [ADR-037](./0037-storage-engine-unter-partitionierung.md)
  (bbolt bleibt, aber eine Datei pro Partition; Engine hinter der Abstraktion lokal
  austauschbar).
- **Cross-Partition-Read-Modell** (globale Order-Projektion, Cross-Partition-Queries)
  → entschieden in [ADR-036](./0036-read-path-cqrs-unter-partitionierung.md)
  (Scatter-Gather + per-Partition-Cursor-Vektor, globale Ordnung als nachgelagertes
  CQRS-Read-Modell; baut auf ADR-017/ADR-029 auf).

**Referenzen**

- ADR-002 (Single-Instance), ADR-003 (serialisierte Schreibvorgänge),
  ADR-012 (Hash-Kette / Tamper-Evidence) — die hier bewusst aufgegebenen bzw.
  umgebauten Garantien.
- ADR-004 (CloudEvents als Event-Format), ADR-005 (Subjects als Stream-Identifier)
  — die fachliche `source`-Achse.
- ADR-006 (Append-only Storage / bbolt), ADR-009 (Group Commit), ADR-017 (CEL),
  ADR-029 (Sekundär-Query auf `event.data`) — die heute betroffenen Mechaniken.
- Chrampfer: Single-Writer-per-Partition mit Group Commit (Vorbild für das Modell).
