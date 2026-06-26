# Implementierungsplan: Partitionierungsmodell für horizontale Skalierung

**Projekt:** `github.com/pblumer/clio`
**Status:** PLANUNG — bereit zur etappenweisen Umsetzung; Etappen 4+ bewusst auf Folge-ADRs gated
**Bezug:** [ADR-034](../adr/0034-partitionierungsmodell-fuer-horizontale-skalierung.md) (Partitionierungsmodell, *vorgeschlagen*). Setzt **ausschließlich** die in ADR-034 getroffene Fundament-Entscheidung um. Storage-Engine, Tamper-Evidence-Anchoring, Read-Path/CQRS und Distribution/Consensus bleiben referenzierten Folge-ADRs vorbehalten und werden hier nur **abgegrenzt**, nicht entschieden.
**Vorbild-Doku:** `docs/plans/security-api-keys-plan.md` (in sich geschlossene Work-Packages mit Akzeptanzkriterien); Abgrenzung zu `docs/plans/storage-scaling-plan.md` (**vertikale** Single-Node-bbolt-Skalierung — orthogonal zu diesem Plan).

---

## 0. Zusammenfassung

ADR-034 ersetzt die **eine** globale, strikt sequenzielle Hash-Chain (ADR-012)
über alle Events durch **n unabhängige Ketten** — eine je Partition, mit eigenem
Writer und eigener per-Partition-Sequenz. Die Partitionszugehörigkeit wird aus dem
CloudEvents-`source` (ADR-004) abgeleitet. Folge: **globale Total-Order entfällt**
(neue Invariante **INV-P1**); das weicht bewusst von ADR-002 (Single-Instance) und
ADR-003 (serialisierte globale Schreibordnung) ab.

Der Plan zerlegt das in **vier Etappen**, von denen die ersten drei **heute,
Single-Node**, umsetzbar und verifizierbar sind — sie liefern die
Partitionierungs-**Invariante** ohne dass schon über mehrere Knoten verteilt wird.
Erst Etappe 4 verteilt physisch und ist auf die noch offenen Folge-ADRs gated.

Diese Reihenfolge ist Absicht: Die folgenschwerste Eigenschaft von ADR-034 —
*globale Total-Order weg, n Ketten statt einer* — ist eine **lokale Refaktorierung
des Store-Kerns** und vollständig im aktuellen Single-Binary (ADR-001) testbar.
Verteilung, Consensus und eine neue Storage-Engine sind separate, jeweils eigene
Entscheidungen; sie vorzuziehen würde Annahmen festschreiben, die ADR-034 bewusst
offen lässt.

### Designentscheidungen, die alles Weitere prägen

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Partition-Key-Quelle | Aus `source` abgeleiteter Stream-/Aggregate-Key | ADR-034; Events eines Aggregats werden gemeinsam gelesen/geordnet (vgl. ADR-005) |
| Mapping `key → partition` | **Konsistentes Hashing** mit fixem virtuellen Ring | ADR-034; Rebalancing mit minimaler Key-Migration; reine, deterministische Funktion |
| Partitionsanzahl (v1) | **Fix, konfigurierbar** (`CLIO_PARTITIONS`, Default `1`) | Default `1` ⇒ verhaltensgleich zu heute; dynamisches Splitting/Merging ist Folge-ADR |
| Physische Ablage (Etappe 1–3) | **Eine bbolt-DB, Partition als Bucket-Präfix** | Kein Storage-Engine-Wechsel vorgreifen (das ist eine eigene Folge-ADR); Invariante zuerst |
| Writer-Modell | **Single-Writer-per-Partition**, Group Commit je Partition (ADR-009) | ADR-034; erprobtes Vorbild (Chrampfer); behält Durability-Optionen `CLIO_SYNC` |
| Globale Order im Read-Path | **Nicht** mehr garantiert; nur per-Partition-Cursor | INV-P1; jede globale Sicht muss explizit als Read-Modell rekonstruiert werden |
| Approx. globale Zeit | Optionales **HLC**-Attribut pro Event (best effort) | ADR-034: globale Reihenfolge *approximierbar*, nicht garantiert; rein additiv |
| Migration Bestand | Greenfield startet partitioniert; **Re-Chaining** des Bestands ist gated | Bestehende eine Kette → n Ketten berührt Tamper-Evidence (ADR-012) → Folge-ADR |

Das Single-Binary-/Stdlib-Prinzip (ADR-001) bleibt in Etappen 1–3 erhalten:
**keine neue externe Abhängigkeit**. Default `CLIO_PARTITIONS=1` macht die
Umstellung **rückwärtskompatibel** — eine einzige Partition ist exakt das heutige
Verhalten (eine Kette, eine Sequenz).

---

## 1. Zielarchitektur (Etappen 1–3, Single-Node)

### 1.1 Partition-Routing (`internal/partition`)

Reines, abhängigkeitsfreies Domänenpaket (analog `internal/auth`): keine Storage-,
keine HTTP-Abhängigkeit.

- `KeyFromSource(source string) (key string)` — leitet den Stream-/Aggregate-Key
  aus dem CloudEvents-`source` ab (Default: `source` selbst; Normalisierung
  dokumentiert).
- `Ring` — konsistenter Hash-Ring mit `N` virtuellen Knoten je Partition;
  `Partition(key string) PartitionID` ist **deterministisch** und stabil gegen
  Prozess-Neustart (Hash: `crypto/sha256`, kein `maphash`/Seed — sonst nicht
  reproduzierbar).
- `Rebalance(old, new RingConfig) []KeyMigration` — Vorschau, welche Keys bei
  Knotenänderung wandern (für die spätere Distribution-ADR; in v1 nur Test-/
  Diagnose-Pfad, kein Live-Move).

### 1.2 Per-Partition-Writer & -Kette (`internal/store`)

Der Store-Kern wird so refaktoriert, dass Sequenz **und** Hash-Chain **per
Partition** geführt werden statt global:

- Jede Partition hat eigenen Bucket-Namespace (`p/<id>/events`, `p/<id>/meta`),
  eigene monoton steigende Sequenz und eigene `prevHash → hash`-Kette (ADR-012,
  pro Partition lückenlos).
- Schreibpfad: `source → KeyFromSource → Ring.Partition → Writer[partition]`.
  Je Partition **ein** Writer mit Group Commit (ADR-009); `CLIO_SYNC` wirkt
  unverändert pro Batch.
- `Verify()` (ADR-012/031) prüft **jede** Kette einzeln und meldet pro Partition
  Lücken/Brüche; ein globaler Verify ist die Konjunktion der Partitions-Verifies.

### 1.3 Read-Path-Anpassung

- `read-events`/`run-query` (ADR-017) liefern Ergebnisse **per Partition geordnet**;
  Cursor werden zu **per-Partition-Cursorn** (`{partition: seq}`-Vektor statt
  skalarer globaler Sequenz).
- Anfragen, die heute implizit globale Total-Order erwarten, bekommen ein
  **dokumentiertes, deterministisches** Merge (z. B. nach HLC/`time`) mit klarer
  Kennzeichnung „approximierte Ordnung, keine Garantie" (INV-P1).
- Volatiler globaler Stream über `observe` interleavt Partitionen best effort.

---

## 2. Work-Packages

> Akzeptanzkriterien sind je WP **prüfbar** formuliert. Qualitätstor je WP:
> `make lint` · `make test` · `make race` grün (SESSION_PROMPT.md §5).

### WP-0 — Konsumenten-Audit (Voraussetzung, blockiert WP-3) — ✅ ABGESCHLOSSEN

Bestandsaufnahme aller Stellen, die **globale Total-Order / eine globale Sequenz**
annehmen (Code, `/ui`, Beispiele, Postman, Doku).

- **Ergebnis:** [`partitioning-consumer-audit.md`](./partitioning-consumer-audit.md)
  — 7× BRICHT (konzentriert im Store-Kern), ~16× BRAUCHT-CURSOR (skalare globale ID
  als Cursor über API/UI/Worker/Postman/Lehrtexte), ~6× nur Doku-Nachzug.
- **Akzeptanz:** ✅ Jeder Treffer ist klassifiziert; keine offene „unklar"-Zeile.
- **Kernerkenntnis:** Der Bruch sitzt konzentriert (ID-Vergabe + Hash-Kette +
  Read-Ordnung an **derselben** globalen Sequenz), aber der externe **Cursor-Vertrag**
  (`lowerBound`, `eventsTotal+1`, Singleton-Checkpoint) ist eine Breaking Change.

### WP-1 — `internal/partition` (Routing, rein) — ✅ ABGESCHLOSSEN

Konsistentes Hashing + Key-Ableitung als reines Paket.

- **Akzeptanz:** ✅ Determinismus-Test (gleiche Eingabe ⇒ gleiche Partition über
  Ring-Neuaufbau, sha256 ohne Seed); ✅ Verteilungs-Test (≈ Gleichverteilung über
  40k deterministische Keys, Toleranz ±35 % dokumentiert); ✅ `Rebalance`-Test
  (N→N+1 ⇒ < 22 % der Keys wandern, plus Gegenprobe gegen naives Modulo); ✅ N=1 ⇒
  immer Partition 0 (opt-in-Skalierung, §4.1); ✅ stdlib-only, keine Storage-/HTTP-
  Importe.
- **Umgesetzt:** `internal/partition` (`KeyFromSource`, `Ring`/`NewRing`,
  `Partition`/`PartitionForSource`, `Rebalance`); `make lint`/`test`/`race` grün.

### WP-2 — Per-Partition-Writer & -Kette (Storage: ADR-037) — ✅ KERN UMGESETZT

Store-Kern auf n Ketten/Sequenzen umgestellt; Storage-Substrat = **eine bbolt-Datei
pro Partition** (ADR-037). Default `CLIO_PARTITIONS=1` = heutiges Verhalten.

**Umgesetzt:** `internal/store/shard.go` (Partition = eigene bbolt-Datei, eigene
Sequenz/Kette); Partition 0 = Basis-Datei + zentrale Buckets (Schemas/Schlüsselbund/
Audit-Log), weitere Partitionen als `<db>.p<id>`. `AppendAuthored` routet nach
`source`, lehnt Mixed-Batches ab (`ErrMixedPartition` → 400); Schema-Validierung
gegen die zentralen Schemas. Alle Aggregat-/Lese-/Verify-Methoden fächern über die
Partitionen (per-Partition-Reihenfolge, INV-P3-Default). Config `CLIO_PARTITIONS`/
`CLIO_PARTITION_VNODES`. Tests (`store_partition_test.go`): n=1-Identität, Routing,
per-Partition-`Verify`, Mixed-Batch-400, Fan-out-Read, paralleler Race-Test. `make
lint`/`test`/`race` grün.

**Bewusst zurückgestellt (Folge-Increments, dokumentiert):** (e) **Handle-Pool**
(lazy open/close, LRU) — derzeit bleiben alle Partitionen offen (einfach & sicher);
**store-weites Backup** über n Dateien ist bei N>1 gesperrt (`ErrBackupMultiPartition`,
n=1 unverändert) → ADR-035-Snapshot-Punkt; der **Skalierungs-Benchmark** (n Writer >
1) ist noch nicht als CI-Bench hinterlegt. (f) Die `storage-scaling-plan`-Hebel wirken
bereits pro Partitionsdatei (`InitialMmapSize`/Compaction je Datei).

- **Akzeptanz:** (a) Mit `CLIO_PARTITIONS=1` sind Hashes/Sequenzen **bit-identisch**
  zum Verhalten vor der Umstellung (Regressions-Golden-Test) — eine Datei, ein
  Writer wie heute. (b) Mit `CLIO_PARTITIONS=8` landen Events nach `source`
  deterministisch in der richtigen Partition/Datei; jede Partition hat eine
  lückenlose Kette (`Verify` grün je Partition). (c) Paralleles Schreiben in
  **verschiedene** Partitionen skaliert (Benchmark: n Writer > 1 Writer Durchsatz,
  **kein** gemeinsamer Datei-Schreib-Lock; Zahlen dokumentiert). (d) `make race` grün
  unter paralleler Schreiblast über mehrere Partitionen. (e) **Handle-Pool** (lazy
  open/close, LRU, konfigurierbar) hält nur aktive Partitionsdateien gemappt;
  Reopen einer „kalten" Partition funktioniert korrekt; Adressraum-/FD-Verbrauch
  bleibt mit der Partitionszahl beschränkt. (f) Die `storage-scaling-plan`-Hebel
  (`InitialMmapSize`-Vorab-Mmap, Headroom-Monitor, Online-Compaction) wirken pro
  Partitionsdatei.

### WP-3 — Read-Path, Scatter-Gather & Cursor (ADR-036, braucht WP-0) — 🟡 TEILWEISE

Realisiert [ADR-036](../adr/0036-read-path-cqrs-unter-partitionierung.md):
Scatter-Gather mit streaming k-Wege-Merge, opaker per-Partition-Cursor-Vektor,
explizite `order`-Klassifikation (INV-P3).

**Mit WP-2 umgesetzt:** Der Store-Lesepfad fächert über alle Partitionen
(`readShards`) in **per-Partition-Reihenfolge** (ADR-036-Default `order:
per-partition`) und streamt je Partition (keine Voll-Materialisierung; Limit/Abbruch
über Partitionsgrenzen).

**WP-3-Rest umgesetzt (Cursor-Vektor-Kern):**
- `event.Event.Partition` — serverseitiges Sicht-Attribut (nicht gespeichert, **nicht
  im Hash**, `omitempty` → n=1 byte-identisch), gesetzt am Lese-/Observe-/Write-Return.
- `ReadOptions.LowerBounds` (partition → untere Grenze) + `forShard` — **per-Partition-
  Cursor** im Fan-out; `Store.Partitions()`/`PartitionOf` als Konsumenten-Helfer.
- **observe** dedupliziert und resümiert per `(partition, seq)` statt skalarer ID;
  neues optionales `cursor`-Feld (`{partition: seq}`), `lowerBound` bleibt n=1-
  abwärtskompatibel; Cursor-Validierung (Out-of-Range → 400).
- Tests: per-Partition-Cursor-Resume, Partition-Sicht-Attribut, `omitempty`-Hash-
  Neutralität, HTTP-Surface (read-events trägt `partition`, Mixed-Batch/Cursor-400).

**Noch offen (eigenes Increment):** Client-Adoption des Cursors — **Dashboard-JS**
(Reconnect baut den Cursor aus `partition`/`id`), **Postman** und das
**`projection-worker`-Beispiel** (per-Partition-Checkpoint); `run-query`-Cursor-Vektor;
die optionale `approximated`-Zeitmischung. Bei n=1 funktionieren alle bestehenden
Clients unverändert weiter.

- **Akzeptanz:** (a) Read/Query mit `source`/Key-Filter bleibt **single-partition**
  (kein Fan-out); ohne solchen Filter fächert er korrekt über die betroffenen
  Partitionen und merged streaming (keine Voll-Materialisierung; ADR-028
  Heartbeat/Deadline gelten pro Partition **und** für den Merge). (b) `read-events`/
  `run-query` liefern je Partition strikt geordnet; jede Mehr-Partition-Antwort trägt
  `order: per-partition|approximated` (nie „global garantiert"). (c) Der **opake
  Cursor-Vektor** round-trippt korrekt (kein Event doppelt/verloren über
  Partitionsgrenzen); bei `CLIO_PARTITIONS=1` ist er bit-kompatibel zum alten
  skalaren `lowerBound`. (d) `/ui`-Explorer zeigt Partition je Event. (e) Alle
  Audit-Treffer aus WP-0 mit Klasse „BRAUCHT-CURSOR"/„BRICHT" im Lesepfad sind
  adressiert — inkl. Umstellung des `projection-worker-postgres`-Beispiels auf
  per-Partition-Checkpoint.

### WP-5 — Globaler Anker / Tamper-Evidence (ADR-035, braucht WP-2)

Pro-Partition-Genesis + append-only Anker-Kette mit Merkle-Commitment über alle
`(partitionID, head, seq)`; Verify-Erweiterung; Epoche-0-Versiegelung des Bestands.

- **Akzeptanz:** (a) Jede Partition verifiziert von ihrem partitionseigenen Genesis.
  (b) Ein Anker reproduziert die Merkle-Wurzel der aktuellen Heads; manipuliert man
  eine Partition (Drop/Rollback) und prüft gegen den letzten Anker → **erkannt**.
  (c) `verify` = Konjunktion der Partitions-Verifies **und** Anker-Reproduktion
  **und** lückenlose Anker-Kette. (d) Migration: bestehende eine Kette bleibt
  unverändert als Epoche-0 prüfbar; ihr Head ist Genesis-Anker (`anchorSeq=0`); kein
  Event wird neu gehasht (ADR-012/015 gewahrt). (e) Anker-Koordinator hält **keinen**
  partitionsübergreifenden Schreib-Lock (read-only Snapshot, vgl. ADR-031).

### WP-4 — Observability & Betrieb

Partition als erste Klasse in Metriken/`/info`.

- **Akzeptanz:** `/metrics` und `/info` weisen je Partition Sequenz-Highwater,
  Schreibrate und Kettenstatus aus; eine **Hot-Partition** ist an den Metriken
  erkennbar (Vorbereitung für die spätere Sub-Partitionierungs-ADR). Doku-Update in
  `ARCHITECTURE.md` (Ist-Zustand) sobald WP-2/3 gemerged sind.

---

## 3. Migration & Rückwärtskompatibilität

- **Neudeployments / leere DB:** starten direkt partitioniert; nichts zu migrieren.
- **Default `CLIO_PARTITIONS=1`:** verhaltensgleich zu heute — bestehende
  Deployments sind ohne Eingriff weiter korrekt; Umstellung auf > 1 ist eine
  bewusste Operator-Entscheidung.
- **Bestehende, gefüllte DB → n Partitionen (Re-Chaining):** **bewusst gated.** Die
  bestehende *eine* Kette in n Ketten umzuhängen erzeugt neue `prevHash`-Verläufe
  und berührt damit direkt Tamper-Evidence (ADR-012) und das Append-only-Versprechen
  (ADR-015). Wie der Bestand verifizierbar überführt (oder über ein übergeordnetes
  Anchoring überspannt) wird, entscheidet die **Folge-ADR „Tamper-Evidence unter
  Partitionierung"** — nicht dieser Plan. Bis dahin: keine In-Place-Migration
  gefüllter Produktiv-DBs; nur Neudeployments oder `CLIO_PARTITIONS=1`.

---

## 4. Bewusste Nicht-Ziele (gehören in Folge-ADRs)

Dieser Plan liefert das **Single-Node-Fundament** der Partitionierung. Ausdrücklich
**nicht** Teil dieses Plans:

- **Physische Verteilung über Knoten, Rebalancing-Live-Move, Consensus** —
  Architektur **entschieden in
  [ADR-038](../adr/0038-distribution-consensus-partition-ownership.md)** (Write-Leases
  + eingebettetes Raft, `static` als Single-Node-Default; INV-P4), aber **Umsetzung ist
  Etappe 4** und bleibt aus diesem (Single-Node-)Plan ausgeklammert. WP-1 liefert nur
  die *reine* Mapping-/Rebalance-Funktion, keinen Live-Datentransport; der `static`
  Coordinator ist der implizite Modus von WP-2.
- **Übergeordnetes Anchoring der n Ketten / Bestands-Migration** → **entschieden in
  [ADR-035](../adr/0035-tamper-evidence-unter-partitionierung.md)** (n Ketten +
  globaler Merkle-Anker, Epoche-0-Versiegelung statt Re-Chaining); umgesetzt in
  **WP-5**, gegen WP-2 gated. Damit **nicht mehr** Nicht-Ziel, sondern Teil dieses
  Plans.
- **Neue Storage-Engine** jenseits bbolt (B+Tree-Grenzen aus ADR-006/dem
  Storage-Scaling-Plan) → Folge-ADR „Storage-Engine".
- **Cross-Partition-Read-Modell / globale Order-Projektion (CQRS)** → Folge-ADR
  „Read-Path / CQRS" (baut auf ADR-017/ADR-029 auf).
- **Dynamisches Splitting/Merging & Sub-Partitionierung heißer Keys** → eigene
  Folge-ADR (WP-4 liefert nur die Erkennbarkeit über Metriken).
- **Verhalten bei Events ohne eindeutigen `source`** → offener Punkt aus ADR-034
  (vgl. Inbox/tokenlose Writes, ADR-026).

---

## 5. Reihenfolge & Gating

```
WP-1 (Routing) ─┐
                ├─► WP-2 (Writer/Kette) ─┬─► WP-3 (Read-Path) ─► WP-4 (Observability)
WP-0 (Audit) ✅ ─┘   (ADR-034)           └─► WP-5 (Anker/Tamper-Evidence, ADR-035)
                                     ▲
                                     └─ WP-0 ✅ blockiert WP-3 (Cursor-Umstellung)

Etappe 4 (physische Verteilung) ── Architektur entschieden (ADR-038: Leases+Raft,
                                    `static` Default); Umsetzung wartet auf realen
                                    Multi-Node-Treiber. Tamper-Evidence (ADR-035),
                                    Storage (ADR-037) und Read-Path (ADR-036) stehen.
```

Empfehlung: WP-0 und WP-1 parallel starten (unabhängig), dann WP-2, dann WP-3/WP-4.
Jede Etappe ist für sich mergebar; ab WP-2 ist `ARCHITECTURE.md` (Ist-Zustand)
nachzuziehen, sobald gemerged.
