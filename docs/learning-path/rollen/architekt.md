# Track: Architekt:in / Entscheider:in 📐

> Du musst entscheiden, *ob* und *wo* Clio (oder Event Sourcing generell) passt.
> Dieser Track fokussiert auf **Trade-offs, Garantien, Grenzen** und die
> Einordnung gegenüber dem Vorbild EventSourcingDB — nicht auf curl-Details.

## Dein Ziel

Du kannst begründet entscheiden, ob Clio für ein Vorhaben taugt, kennst die
bewussten Vereinfachungen (Non-Goals) und ihre Konsequenzen, und kannst die
Garantien (Ordnung, Atomarität, Tamper-Evidence) sauber erklären.

## Voraussetzungen

- [Grundlagen 1 — Was ist Event Sourcing?](../00-grundlagen/01-was-ist-event-sourcing.md)
- Bereitschaft, [`ARCHITECTURE.md`](../../../ARCHITECTURE.md) als Hauptquelle zu
  lesen (besonders §2 Ziele/Non-Goals und §7 ADRs).

## Reihenfolge

| # | Lektüre / Modul | Worauf achten |
|---|---|---|
| 1 | [`ARCHITECTURE.md` §1–§2](../../../ARCHITECTURE.md#1-worum-geht-es-elevator-pitch) | Elevator Pitch, Ziele, **Non-Goals** |
| 2 | [M04 — Optimistic Concurrency](../module/M04-optimistic-concurrency.md) | Wie Konsistenz/Invarianten ohne Transaktionen über Aggregate hinweg funktionieren |
| 3 | [M07 — Integrität & Signaturen](../module/M07-integritaet-und-signaturen.md) | Tamper-Evidence (Hash-Kette) vs. Authentizität (Ed25519) |
| 4 | [`ARCHITECTURE.md` §7 ADRs](../../../ARCHITECTURE.md#7-architecture-decision-records-adrs) | Die Entscheidungen *und ihre Konsequenzen* |

## Die entscheidenden Trade-offs

| Entscheidung | Gewinn | Preis | ADR |
|---|---|---|---|
| **Single-Instance** | drastisch einfachere Ordnung/Atomarität | keine HA, keine horizontale Skalierung | [002](../../../ARCHITECTURE.md#adr-002-single-instance-architektur-vorerst-kein-clustering) |
| **Serialisierte Writes** | strikte globale Ordnung ohne Konsens | Schreibdurchsatz limitiert | [003](../../../ARCHITECTURE.md#adr-003-serialisierte-schreibvorgänge-für-ordnung--atomarität) |
| **Group Commit (Default)** | hoher Durchsatz bei voller Durability | höhere Latenz bei Einzel-Writes | [009](../../../ARCHITECTURE.md#adr-009-group-commit-als-default-schreibstrategie) |
| **CEL statt EventQL** | Bruchteil des Aufwands, reife Engine | keine Byte-Kompatibilität zu EventSourcingDB | [017](../../../ARCHITECTURE.md#adr-017-abfrageschicht-auf-cel-statt-eigener-eventql-sprache) |
| **Einzel-Token-Auth** | minimaler Aufwand | kein RBAC, keine Mandantentrennung | [008](../../../ARCHITECTURE.md#adr-008-authentifizierung-über-einzelnes-api-token) |
| **Hash-Kette + Ed25519** | beweisbare Integrität *und* Urheberschaft | eigenes Kanonisierungsschema (nicht byte-kompatibel) | [012](../../../ARCHITECTURE.md#adr-012-hash-kette-für-tamper-evidence) / [016](../../../ARCHITECTURE.md#adr-016-ed25519-signaturen-für-authentizität) |

## Wann Clio passt — und wann nicht

**Passt gut, wenn:**

- du eine **auditierbare, unveränderliche Historie** brauchst,
- eine **Single-Instance** mit Volume akzeptabel ist (kein HA-Zwang),
- Projektionen/Geschäftslogik in deiner Anwendung leben,
- ein schlankes, abhängigkeitsfreies Binary ein Vorteil ist.

**Passt (noch) nicht, wenn:**

- du **Hochverfügbarkeit/Clustering** brauchst (Non-Goal, ADR-002),
- du **feingranulares RBAC/Mandantentrennung** auf DB-Ebene brauchst (ADR-008),
- du **strikte EventQL-Byte-Kompatibilität** zu EventSourcingDB brauchst
  (bewusst nicht, ADR-017).

## Abgrenzung zum Vorbild

Clio ist eine **unabhängige** Implementierung, funktional an EventSourcingDB
orientiert — *kein* Fork, kein übernommener Code; nur öffentlich dokumentierte
Konzepte/API-Formate werden nachgebildet (siehe
[`ARCHITECTURE.md` §10](../../../ARCHITECTURE.md#10-hinweise-zur-pflege-dieses-dokuments)).
Motiv u. a.: das Original ist nur bis 25.000 Events kostenlos
([§2.1](../../../ARCHITECTURE.md#21-warum-dieses-projekt)).

## Geschafft, wenn…

Du in zwei Minuten erklären kannst, welche **Garantien** Clio gibt, welche
**bewussten Vereinfachungen** das ermöglicht und für welchen konkreten
Anwendungsfall du Clio empfehlen würdest — oder eben nicht.
