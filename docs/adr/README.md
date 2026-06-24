# Architecture Decision Records (ADRs)

Dieses Verzeichnis enthält die **Architektur-Entscheidungen** von `cliostore` —
das *Warum* hinter dem System. Es ergänzt die [`ARCHITECTURE.md`](../../ARCHITECTURE.md)
(aktueller Soll-/Ist-Zustand) und die [`docs/plans/`](../plans/) (Umsetzungspläne).
Die Einordnung der drei Quellen und die Arbeitsregeln stehen in der
[`AGENTS.md`](../../AGENTS.md) im Repo-Wurzelverzeichnis.

## Konvention

- **Eine Datei pro Entscheidung**, Schema `NNNN-kebab-case-titel.md`
  (Dateiname **vierstellig** zur Sortierung; die ADR-ID im Text bleibt
  **dreistellig** wie im Bestand, z. B. „ADR-034").
- Neue ADRs entstehen aus [`_template.md`](./_template.md).
- **Diese Tabelle ist die Single Source of Truth für vergebene Nummern.** Vor dem
  Anlegen einer neuen ADR hier die nächste freie Nummer ziehen und sofort eintragen.
- **Status-Vokabular** (aus `ARCHITECTURE.md` §7): `Vorgeschlagen`, `Akzeptiert`,
  `Abgelöst durch ADR-MMM`, `Verworfen`. Eine angenommene Entscheidung wird **nie
  editiert, um sie zu ändern** — stattdessen neue ADR; die alte auf „Abgelöst
  durch ADR-MMM" setzen (siehe `ARCHITECTURE.md` §10).

### Historischer Hinweis zum Fundort der Bodies

ADR-001…033 wurden historisch **als ausformulierte Abschnitte direkt in
[`ARCHITECTURE.md` §7](../../ARCHITECTURE.md#7-architecture-decision-records-adrs)**
geführt, nicht als Einzeldateien. Diese Bodies sind die maßgebliche Quelle und
werden **nicht** rückwirkend in Einzeldateien kopiert (keine Divergenz, keine
erfundenen Inhalte). Die Spalte **Pfad** zeigt daher für Bestands-ADRs auf
`ARCHITECTURE.md §7`. Ab sofort gilt für **neue** Entscheidungen: eine Datei pro
ADR in diesem Verzeichnis. ADR-026 wurde als erste Entscheidung in eine Einzeldatei
ausgelagert (sie war als einzige noch „Vorgeschlagen" und damit in Bewegung).

Datums-Spalte: Die Bestands-ADRs in §7 tragen **kein** Einzeldatum; dort steht `—`
(nicht rückwirkend erfunden).

## Index

| Nr. | Titel | Status | Datum | Pfad |
|---|---|---|---|---|
| ADR-001 | Implementierungssprache Go | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-002 | Single-Instance-Architektur (vorerst kein Clustering) | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-003 | Serialisierte Schreibvorgänge für Ordnung & Atomarität | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-004 | CloudEvents als Event-Format (strukturiertes JSON) | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-005 | Subjects als hierarchische Stream-Identifier | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-006 | Append-only Storage mit In-Memory-Index | Akzeptiert (Stufe 0–1) | — | `ARCHITECTURE.md` §7 |
| ADR-007 | EventQL als spätes, optionales Ziel | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-008 | Authentifizierung über einzelnes API-Token | Akzeptiert (erweitert durch ADR-025) | — | `ARCHITECTURE.md` §7 |
| ADR-009 | Group Commit als Default-Schreibstrategie | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-010 | Komfort-Leseroute `GET /api/v1/events/<subject>` | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-011 | Eingebettete OpenAPI-Spec + Swagger UI | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-012 | Hash-Kette für Tamper-Evidence | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-013 | Eigene, abhängigkeitsfreie Metriken statt Prometheus-Client | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-014 | Event-Schemas via JSON Schema | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-015 | Kompaktierung defragmentiert, löscht aber keine Events | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-016 | Ed25519-Signaturen für Authentizität | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-017 | Abfrageschicht auf CEL statt eigener EventQL-Sprache | Akzeptiert, in Umsetzung | — | `ARCHITECTURE.md` §7 |
| ADR-018 | Bewusste Abweichungen von den Swiss API Guidelines | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-019 | Swiss-Guidelines Quick Wins — problem+json & Cache-Control | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-020 | Eingebettetes Betriebs-Dashboard unter `/ui` | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-021 | Typ-Index für `run-query` | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-022 | Dev-Mode mit destruktivem DB-Reset und gated Bulk-Import-Fenster | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-023 | Kostenbasierte Index-Wahl für `run-query` (Subject vs. Typ) | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-024 | Transparente Wert-Kompression der Event-Ablage (DEFLATE + Preset-Dictionary) | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-025 | Mehrere benannte API-Keys mit Scopes, Widerruf und Audit | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-026 | Authentifizierte Event-Herkunft über Tokens | Vorgeschlagen | — | [`0026-authentifizierte-event-herkunft-ueber-tokens.md`](./0026-authentifizierte-event-herkunft-ueber-tokens.md) (Body auch in `ARCHITECTURE.md` §7) |
| ADR-027 | *(reserviert)* Token-Lifecycle: Tabelle vs. interner Event-Stream | Offen — noch nicht entschieden (Folge-ADR aus ADR-026) | — | — |
| ADR-028 | `run-query`-Resilienz unter Last — Heartbeat, Query-Deadline & Index-Warnung | Akzeptiert | — | `ARCHITECTURE.md` §7 |
| ADR-029 | Sekundär-Query auf `event.data` — interner Feld-Index zuerst, externes Read-Model zurückgestellt | Akzeptiert (interner Index v1 umgesetzt) | — | `ARCHITECTURE.md` §7 |
| ADR-030 | Aktivität & Presence — wer ist online, wer tut was | Akzeptiert (umgesetzt) | — | `ARCHITECTURE.md` §7 |
| ADR-031 | Backup/Restore/Verify über konsistente bbolt-Snapshots; PITR optional | Akzeptiert (Stufe 1 umgesetzt) | — | `ARCHITECTURE.md` §7 |
| ADR-032 | Persistentes Audit-Log administrativer Aktionen (separater bbolt-Bucket) | Akzeptiert (umgesetzt) | — | `ARCHITECTURE.md` §7 |
| ADR-033 | Subject-/Prefix-basierte Scopes (`read:/orders/*`) | Akzeptiert (umgesetzt) | — | `ARCHITECTURE.md` §7 |
| ADR-034 | Partitionierungsmodell für horizontale Skalierung | Vorgeschlagen | 2026-06-24 | [`0034-partitionierungsmodell-fuer-horizontale-skalierung.md`](./0034-partitionierungsmodell-fuer-horizontale-skalierung.md) |
| ADR-035 | Tamper-Evidence unter Partitionierung (n Ketten + globaler Anker) | Vorgeschlagen | 2026-06-24 | [`0035-tamper-evidence-unter-partitionierung.md`](./0035-tamper-evidence-unter-partitionierung.md) |

**Nächste freie Nummer: ADR-036.** (ADR-027 ist als Folge-ADR aus ADR-026
reserviert, aber noch unentschieden — nicht neu vergeben.)
