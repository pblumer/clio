# ADR-038: Distribution & Consensus — Partition-Ownership, Leases & Rebalancing

> **Kontext-Cluster:** Verteiltes System / Skalierung. Letzte Folge-ADR zu
> [ADR-034](./0034-partitionierungsmodell-fuer-horizontale-skalierung.md) — sie löst
> die offenen Punkte „exaktes `source → partition`-Mapping und Rebalancing-Strategie"
> sowie die in ADR-035/036 offen gelassene verteilte Koordination. Entscheidet
> Platzierung, Eigentümerschaft und Umverteilung von Partitionen über Knoten.

**Status:** Vorgeschlagen

**Datum:** 2026-06-24

**Kontext**

ADR-034 partitioniert den Store und verlangt **genau einen Writer pro Partition**
(INV-P1). Solange Clio Single-Instance ist (ADR-002), ist das trivial: ein Prozess
besitzt alle Partitionen. Sobald Partitionen über **mehrere Knoten** verteilt werden
— der eigentliche Zweck von ADR-034 (horizontaler Schreibdurchsatz) — entsteht das
**Kernproblem verteilter Systeme**: Es muss systemweit **eindeutig** sein, welcher
Knoten eine Partition beschreibt. Zwei Knoten, die gleichzeitig dieselbe Partition
beschreiben (Split-Brain), zerstören die per-Partition-Kette und damit INV-P1/INV-P2.

Drei Dinge sind zu entscheiden:

1. **Mapping** `source → partition` (konkret; ADR-034 legte „konsistentes Hashing"
   als Richtung fest).
2. **Platzierung & Eigentümerschaft** `partition → node` mit der Sicherheits­garantie
   „höchstens ein Writer pro Partition, global".
3. **Rebalancing** bei Knoten-Beitritt/-Ausfall mit minimaler Migration.

Das steht in scharfer Spannung zu **ADR-001** (Abhängigkeitsarmut, pure-Go,
Single-Binary) und **ADR-002** (bewusst „vorerst kein Clustering"). Diese ADR ist —
zusammen mit ADR-034 — diejenige, die ADR-002 ablösen würde. Sie darf das Prinzip
nicht leichtfertig opfern: Konsens „selbst zu bauen" ist notorisch fehleranfällig,
ein **externer** Koordinationsdienst (etcd/ZooKeeper/Consul) widerspricht dem
Single-Binary-Betrieb.

**Entscheidung**

1. **Mapping: konsistentes Hashing mit virtuellen Knoten, deterministisch.** Der aus
   `source` abgeleitete Key (ADR-034/WP-1) wird per stabiler Hash-Funktion
   (`crypto/sha256`, kein geseedeter Hash) auf einen Ring mit `V` virtuellen Knoten
   pro Partition abgebildet. Die Funktion ist rein und reproduzierbar (bereits als
   `internal/partition` in WP-1 vorgesehen). Partitionsanzahl fix pro ADR-034.

2. **Eigentümerschaft ist ein zeitlich begrenztes Write-Lease — höchstens ein Halter
   global.** Eine Partition ist auf einem Knoten **nur dann beschreibbar**, wenn er
   ein **gültiges, ablaufendes Lease** für diese Partition hält. Das Lease, nicht die
   Routing-Tabelle, ist die Sicherheitsgrenze: Schreibannahme setzt ein lokal
   gültiges Lease voraus; läuft es ab oder geht es verloren, **stoppt** der Knoten
   sofort die Writes dieser Partition (fail-stop), bevor ein anderer es übernehmen
   kann (Lease-Dauer > maximale Uhren-/Netzwerk-Drift, fencing token). Das macht
   INV-P1 im verteilten Fall **erzwingbar** statt nur angenommen.

3. **Koordination hinter einer Schnittstelle; Single-Node ist die No-Op-Implementierung;
   Default für Multi-Node ist eingebettetes, pure-Go Raft — kein externer Dienst.**
   Membership und Lease-Zuteilung laufen über eine `Coordinator`-Schnittstelle mit
   zwei Implementierungen:
   - **`static` (Default, Single-Node):** ein Knoten besitzt alle Partitionen, keine
     Konsens-Logik, **keine** neue Abhängigkeit — exakt das heutige Verhalten
     (ADR-002 bleibt für diesen Modus gültig).
   - **`raft` (Multi-Node, opt-in):** eine **eingebettete, pure-Go** Raft-Gruppe
     (etablierte Bibliothek, **nicht** selbst implementiert) führt eine replizierte
     Membership- und Lease-Tabelle. Pure-Go/Single-Binary (ADR-001) bleibt gewahrt:
     keine externe Komponente zu betreiben. Ein **externer** Koordinationsdienst wird
     **verworfen** (operativ schwerer, gegen Single-Binary).

   Konsens regelt **nur** Membership + Lease-Zuteilung (kleiner, seltener
   Zustand) — **nicht** den Event-Schreibpfad. Der Datenpfad bleibt
   Single-Writer-per-Partition ohne Pro-Event-Konsens (sonst fiele der
   Durchsatz-Gewinn aus ADR-034 weg).

4. **Rebalancing = Bewegung ganzer Partitionsdateien, minimal dank konsistentem
   Hashing.** Bei Knotenänderung weist der Ring eine **minimale** Menge Partitionen
   neu zu. Eigentümerwechsel: (a) alter Halter versiegelt/flusht die Partition und
   **gibt das Lease frei**, (b) die **Partitionsdatei** (Einheit aus ADR-037,
   file-per-partition) wird zum neuen Halter transferiert, (c) neuer Halter **erwirbt
   das Lease** und nimmt den Single-Writer-Betrieb wieder auf. Die Dateigranularität
   aus ADR-037 macht die Partition zur sauberen Transport-Einheit.

5. **Routing.** Jeder Knoten kennt über die replizierte Tabelle (bzw. `static`) den
   aktuellen Eigentümer und leitet Writes an ihn weiter; Reads fächern per
   Scatter-Gather (ADR-036) an die Eigentümer der betroffenen Partitionen. Die
   read-only Anker-Sicht (ADR-035) liest die Heads der Eigentümer — konsistent, ohne
   Schreib-Konsens.

**Konsequenzen**

*Positiv (gewonnen):*

- INV-P1 ist im verteilten Fall **erzwingbar** (Lease + fencing), nicht nur
  angenommen — Split-Brain-Doppelschreiber sind ausgeschlossen.
- **ADR-001 bleibt gewahrt:** Default `static` braucht keine neue Abhängigkeit;
  `raft` ist eingebettet/pure-Go, kein externer Dienst. Single-Binary-Betrieb bleibt.
- Konsens nur über kleinen Membership-/Lease-Zustand → der Event-Datenpfad behält den
  vollen partitionierten Durchsatz (ADR-034) ohne Pro-Event-Konsens.
- Rebalancing ist dank file-per-partition (ADR-037) + konsistentem Hashing minimal
  und transportiert klar abgegrenzte Einheiten.
- Sauberer Migrationspfad: `static` = heutiges Single-Instance (ADR-002); Cluster ist
  ein bewusstes Opt-in, kein erzwungener Umbau.

*Negativ / Grenzen (geopfert):*

- **`raft` ist die operativ schwerste Erweiterung des Projekts** (Membership,
  Failure-Detection, Leader-Wahl, Lease-Verwaltung). Auch eingebettet bringt verteilter
  Betrieb echte Komplexität (Quorum, Netzwerk-Partitionen, Uhren-Annahmen).
- **Verfügbarkeit vs. Sicherheit:** Beim Lease-Verlust **stoppt** der alte Halter
  (fail-stop) — bis das Lease neu zugeteilt und ggf. die Datei transferiert ist, ist
  die Partition **nicht beschreibbar**. Korrektheit geht vor Verfügbarkeit.
- **Datentransfer beim Rebalancing** kann groß sein (ganze Partitionsdatei). Bei sehr
  großen Partitionen ist das spürbar → Wechselwirkung mit Sub-Partitionierung.
- **Replikation/Hochverfügbarkeit pro Partition ist NICHT Teil dieser ADR.** Diese ADR
  entscheidet Platzierung/Eigentümerschaft/Umverteilung, **nicht** Datenredundanz. Ein
  Knoten-Ausfall bedeutet ohne separate Replikation potenziellen Datenverlust der von
  ihm gehaltenen Partitionen bis zum Restore (ADR-031). Replikation ist ein eigenes,
  späteres ADR.
- **Die konkrete Raft-Bibliothek ist bewusst noch nicht festgenagelt** (s. offene
  Punkte) — die *Architektur* (Lease + pluggable Coordinator) steht, die *Engine-Wahl*
  wartet auf einen realen Multi-Node-Treiber, um ADR-001 nicht verfrüht aufzugeben.

**Invariante (neu)**

> **INV-P4:** Zu jedem Zeitpunkt hält **höchstens ein** Knoten ein gültiges Write-Lease
> für eine gegebene Partition, und **nur** der Lease-Halter darf in diese Partition
> schreiben. Verliert ein Knoten das Lease (Ablauf, Partition, Neuverteilung), stoppt
> er die Writes dieser Partition, **bevor** ein anderer Knoten das Lease erwerben kann
> (fail-stop mit fencing). Damit ist „Single-Writer-per-Partition" (INV-P1) auch im
> verteilten Betrieb erzwungen, nicht nur angenommen.

**Umsetzung**

Diese ADR betrifft **Etappe 4** des Umsetzungsplans
[`docs/plans/partitioning-plan.md`](../plans/partitioning-plan.md) (physische
Verteilung), die bewusst **nach** den Single-Node-Etappen 1–3/5 liegt. Der `static`
Coordinator ist bereits der implizite Modus von WP-2 (ein Knoten, alle Partitionen);
der `raft`-Modus, Lease-Verwaltung und Datei-Transfer sind eigene Work-Packages der
Etappe 4 und werden erst bei einem konkreten Multi-Node-Bedarf ausgearbeitet.

**Offene Punkte / Folge-ADRs**

- **Konkrete Raft-Bibliothek** (z. B. `hashicorp/raft` vs. `etcd-io/raft`) — Auswahl
  mit Eignungs-/Wartungs-/Lizenz-Bewertung, sobald ein realer Multi-Node-Treiber
  existiert. Bis dahin bleibt `static` der einzige umgesetzte Modus.
- **Replikation / Hochverfügbarkeit pro Partition** (Raft-per-Partition? Follower-
  Replikate? synchron/asynchron?) — eigenes ADR; diese ADR setzt nur Eigentümerschaft.
- **Lease-Parameter** (Dauer, Erneuerung, fencing-Token-Format, Uhren-Annahmen).
- **Datei-Transfer-Mechanik** beim Rebalancing (Streaming-Kopie, Drosselung,
  Wiederaufnahme) — Wechselwirkung mit dem konsistenten Snapshot aus ADR-037.
- **Sub-Partitionierung heißer Keys** (ADR-034-Folge): Split + Neuzuteilung im Ring.

**Referenzen**

- ADR-034 (Partitionierungsmodell, INV-P1) — diese ADR macht INV-P1 verteilt
  erzwingbar (INV-P4) und löst dessen Mapping-/Rebalancing-Punkt.
- ADR-002 (Single-Instance, „vorerst kein Clustering") — der `static`-Modus ist genau
  dieser Zustand; bei Annahme des Clusters (034–038) würde ADR-002 abgelöst.
- ADR-001 (Abhängigkeitsarmut, pure-Go, Single-Binary) — tragender Grund für
  eingebettetes Raft statt externem Dienst und für den abhängigkeitsfreien Default.
- ADR-037 (file-per-partition) — macht die Partition zur Transport-Einheit beim
  Rebalancing.
- ADR-035 (Tamper-Evidence / read-only Anker-Sicht) und ADR-036 (Scatter-Gather-Reads)
  — beide konsumieren die Eigentümer-/Routing-Tabelle dieser ADR.
