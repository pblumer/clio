# ADR-036: Read-Path & CQRS unter Partitionierung (Scatter-Gather + Cursor-Vektor)

> **Kontext-Cluster:** Verteiltes System / Skalierung. Folge-ADR zu
> [ADR-034](./0034-partitionierungsmodell-fuer-horizontale-skalierung.md) — sie löst
> den dort offen gelassenen Punkt „Cross-Partition-Read-Modell / globale
> Order-Projektion". Entscheidet **ausschließlich** den Lesepfad unter
> Partitionierung; Tamper-Evidence ist in ADR-035 entschieden, Storage-Engine und
> Distribution/Consensus bleiben eigenen ADRs.

**Status:** Vorgeschlagen

**Datum:** 2026-06-24

**Kontext**

ADR-034 gibt die globale Total-Order auf (INV-P1); der Konsumenten-Audit
([`docs/plans/partitioning-consumer-audit.md`](../plans/partitioning-consumer-audit.md))
hat gezeigt, dass der **gesamte externe Lese-Vertrag** daran hängt:

- **Cursor.** `observe`/`read-events` nutzen heute eine **skalare, global dichte,
  numerisch vergleichbare** Event-ID als Cursor (`lowerBound`/`upperBound`,
  Dashboard `eventsTotal+1`, Projection-Worker `last_event_id`-Singleton). Mit
  per-Partition-Sequenzen ist diese ID nicht mehr global vergleichbar.
- **Rekursive Reads & Queries.** `read-events` über einen Subject-Subtree und
  `run-query` (CEL, ADR-017) laufen heute als **ein** Scan über den globalen
  `events`-Bucket, indexgestützt (Subject-/Typ-Index ADR-021/023, Datenfeld-Index
  ADR-029) und in globaler Sequenz geordnet. Da partitioniert wird nach `source`,
  verteilt sich ein Subtree-Read auf **mehrere** Partitionen.
- **Ergebnis-Ordnung.** Die heutige Garantie „rekursive Reads bewahren die globale
  Ordnung" existiert nicht mehr.
- **Globale Konsumenten.** Konsumenten, die *eine* total geordnete Sicht brauchen
  (Projektionen, Reporting), müssen laut INV-P1 diese explizit rekonstruieren.

Diese ADR entscheidet, **wie** der Lesepfad unter Partitionierung aussieht und **wo**
die Verantwortung für globale Ordnung liegt — ohne die heutige
`run-query`-Resilienz (Heartbeat/Deadline, ADR-028) und Speicherbeschränkung
aufzugeben.

**Entscheidung**

1. **Reads sind Partition-Scatter-Gather.** Eine Lese-/Query-Operation, die mehrere
   Partitionen betrifft (rekursiver Subtree-Read, `run-query` ohne single-source
   Filter), **fächert** auf die betroffenen Partitionen auf, führt **pro Partition
   lokal** aus (mit den bestehenden Indizes ADR-021/023/029 je Partition) und
   **merged** die Teilströme. Betrifft eine Anfrage nur **eine** Partition (Filter
   bindet die `source`/den Key), entfällt das Fan-out — das ist der häufige,
   günstige Fall (Events eines Aggregats liegen per Konstruktion in einer Partition,
   ADR-034).

2. **Merge ist streaming, nicht materialisierend.** Die Teilströme werden als
   sortierte Läufe **k-Wege-gemerged** (Heap), nicht vollständig in den Speicher
   geladen. Das hält die im Audit/ARCHITECTURE benannte OOM-Gefahr bei 10⁹+ Events
   in Schach und erhält die `run-query`-Resilienz (ADR-028: Heartbeat/Deadline gelten
   pro Partition **und** für den Merge).

3. **Ergebnis-Ordnung ist explizit klassifiziert.** Jede Antwort, die mehr als eine
   Partition berührt, trägt ein Feld `order`:
   - `per-partition` — je Partition strikt geordnet, partitionsübergreifend
     **unbestimmt** (Default, billigster Merge).
   - `approximated` — partitionsübergreifend nach `time`/HLC gemischt, ausdrücklich
     **ohne** Garantie (INV-P1). Nie als „global geordnet" deklariert.
   Eine **strikte** globale Total-Order liefert der Store **nicht** (siehe Punkt 5).

4. **Cursor ist ein opaker per-Partition-Vektor.** Der skalare `lowerBound`/
   `upperBound`-Vertrag wird durch ein **opakes Cursor-Token** ersetzt, das intern
   `{partition: seq}` für die betroffenen Partitionen kodiert. Clients behandeln es
   als undurchsichtig (kein Rechnen mit `+1`). Round-trip-stabil: kein Event doppelt,
   keines verloren über Partitionsgrenzen. **Abwärtskompatibilität:** Bei
   `CLIO_PARTITIONS=1` ist das Token exakt der alte skalare Cursor (ein
   Vektor-Eintrag), sodass Bestands-Clients unverändert funktionieren.

5. **Globale Total-Order ist ein nachgelagertes Read-Modell (CQRS), kein
   Store-Feature.** Der Store verantwortet **per-Partition geordnete Ströme + den
   Cursor-Vektor** — mehr nicht. Konsumenten, die eine einzige total geordnete Sicht
   brauchen, **rekonstruieren** sie in einem nachgelagerten Read-Modell (Projektion).
   **Ob** Clio später ein solches Order-Read-Modell *intern* anbietet (analog zur
   internen-zuerst-Entscheidung in ADR-029) oder es extern bleibt, ist **bewusst
   zurückgestellt** — diese ADR legt nur fest, dass globale Ordnung **außerhalb** der
   Store-Lesegarantie liegt.

6. **Konsumenten-Vertrag: Checkpoint = Cursor-Vektor, Idempotenz pro Partition.** Ein
   Konsument persistiert den **Cursor-Vektor** als Checkpoint (nicht einen skalaren
   `last_event_id`) und führt Idempotenz **pro Partition**. Das löst direkt den im
   Audit als größten Einzel-Brecher markierten Singleton-Checkpoint des
   `projection-worker-postgres`-Beispiels.

**Konsequenzen**

*Positiv (gewonnen):*

- Der häufige Fall (Read/Query gebunden an eine `source`/ein Aggregat) bleibt
  **single-partition** und damit so günstig wie heute — Partitionierung kostet hier
  nichts.
- Cross-Partition-Reads skalieren über paralleles Scatter-Gather; der Merge ist
  streaming und behält die Resilienz-Garantien (ADR-028).
- Der Lese-Vertrag wird **ehrlich**: `order`-Klassifikation macht INV-P1 an der
  API sichtbar, statt globale Ordnung stillschweigend vorzutäuschen.
- Abwärtskompatibler Cursor bei `CLIO_PARTITIONS=1` hält Bestands-Clients am Leben.

*Negativ / Grenzen (geopfert):*

- **Fan-out-Latenz = langsamste Partition** (Tail-Latency) bei breiten
  Cross-Partition-Reads. Mitigation/Granularität ist Sache der Distribution-ADR.
- **Sortierte Cross-Partition-Queries sind nur approximierbar** (`approximated`) oder
  verlangen ein nachgelagertes Read-Modell. „ORDER BY über alles, exakt global" gibt
  es nicht mehr.
- Der **Cursor wird komplexer** (Vektor statt Skalar); Clients müssen ihn opak
  behandeln. Doku/Beispiele/Postman/Lehrtexte müssen umgestellt werden (Audit §3).
- Eine **single-source-Bindung** zur Vermeidung des Fan-outs setzt voraus, dass die
  Query nach `source`/Key filtert; CEL-Queries ohne solchen Filter zahlen den vollen
  Scatter-Gather-Preis über alle Partitionen.

**Invariante (neu)**

> **INV-P3:** Der Lesepfad garantiert strikte Ordnung **nur innerhalb** einer
> Partition. Jede Antwort, die mehrere Partitionen berührt, deklariert ihre Ordnung
> explizit als `per-partition` oder `approximated` und nie als global garantiert.
> Resume/Replay erfolgt über einen **opaken per-Partition-Cursor-Vektor**; Konsumenten
> checkpointen diesen Vektor und führen Idempotenz pro Partition. Eine strikte globale
> Total-Order entsteht ausschließlich in einem nachgelagerten Read-Modell (CQRS),
> nicht im Store (verfeinert INV-P1 für den Lesepfad).

**Umsetzung**

Realisiert in **WP-3** (Read-Path & Cursor) des Umsetzungsplans
[`docs/plans/partitioning-plan.md`](../plans/partitioning-plan.md); der
Scatter-Gather-Query-Teil und die `order`-Klassifikation sind dort Teil der
WP-3-Akzeptanzkriterien. WP-3 ist gegen WP-0 (Audit, erledigt) und WP-2
(Per-Partition-Writer) gated.

**Offene Punkte / Folge-ADRs**

- **Internes Order-Read-Modell ja/nein** (analog ADR-029: intern-zuerst vs. extern) —
  bewusst zurückgestellt, bis ein konkreter Bedarf eine exakte globale Ordnung
  rechtfertigt.
- **Kosten-/Index-Modell beim Scatter-Gather:** Wie die kostenbasierte Index-Wahl
  (ADR-023) auf n Partitionen verallgemeinert wird; Partition-Pruning anhand des
  `source`-Filters, bevor gefächert wird.
- **Fan-out-Begrenzung & Tail-Latency** (Teilanfragen, Timeouts pro Partition) — hängt
  an Partitions-Granularität/Verteilung → Distribution/Consensus-ADR.
- **Konkrete Cursor-Token-Kodierung** (Format, Versionierung, Größe bei vielen
  Partitionen) — Detail für WP-3.

**Referenzen**

- ADR-034 (Partitionierungsmodell, INV-P1) — diese ADR verfeinert INV-P1 für den
  Lesepfad zu INV-P3 und löst dessen Read-Path/CQRS-Punkt.
- ADR-017 (CEL-Abfragen), ADR-021 (Typ-Index), ADR-023 (kostenbasierte Index-Wahl),
  ADR-029 (Sekundär-Query auf `event.data`) — je Partition unverändert angewandt; die
  Index-Wahl wird auf Scatter-Gather verallgemeinert.
- ADR-028 (`run-query`-Resilienz: Heartbeat/Deadline) — gilt pro Partition und für den
  Merge.
- ADR-035 (Tamper-Evidence unter Partitionierung) — Schwester-Folge-ADR; gemeinsam
  decken sie Schreib-Integrität (035) und Lese-Semantik (036) der Partitionierung ab.
- Konsumenten-Audit [`docs/plans/partitioning-consumer-audit.md`](../plans/partitioning-consumer-audit.md) — §3 (BRAUCHT-CURSOR) ist die direkte Eingabe für Punkt 4 & 6.
