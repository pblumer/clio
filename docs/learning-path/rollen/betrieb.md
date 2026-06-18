# Track: Betrieb / DevOps / SRE ⚙️

> Du musst Clio **betreiben** — ausrollen, konfigurieren, überwachen, sichern.
> Dieser Track ist auf Deployment, Durability-Tuning, Observability und Wartung
> fokussiert. Anwendungsdetails (CEL, Schemas) brauchst du nur am Rande.

## Dein Ziel

Du kannst Clio produktionsnah betreiben: als Binary oder Container ausrollen,
die richtige `CLIO_SYNC`-Strategie wählen, Metriken scrapen, Backups ziehen,
die Datei kompaktieren und die Integrität verifizieren.

## Voraussetzungen

- [Grundlagen 2 — Quickstart](../00-grundlagen/02-clio-quickstart.md)
  (bauen/starten/`info`).
- Vertrautheit mit Shell, Env-Variablen, Docker und idealerweise Prometheus.

## Reihenfolge

| # | Modul | Du lernst… |
|---|---|---|
| 1 | [M08 — Betrieb & Durability](../module/M08-betrieb-und-durability.md) | Binary/Docker-Deploy, alle `CLIO_*`-Variablen, `CLIO_SYNC`-Trade-offs, `compact`, Backup |
| 2 | [M09 — Observability](../module/M09-observability.md) | strukturierte Logs, `/metrics`, was zu alarmieren ist |
| 3 | [M07 — Integrität & Signaturen](../module/M07-integritaet-und-signaturen.md) | `verify` als Betriebs-Check, Signing-Key-Verwaltung |

## Betriebs-Checkliste (Kurzfassung)

- [ ] `CLIO_BOOTSTRAP_ADMIN_KEY` als Secret gesetzt (nie im Image/Repo); weitere API-Keys mit Scopes zur Laufzeit angelegt (`CLIO_API_TOKEN` nur noch deprecated).
- [ ] Datenverzeichnis als **persistentes Volume** gemountet (`CLIO_DB_PATH`/`/data`).
- [ ] `CLIO_SYNC` passend zur Last gewählt (Default `group`; siehe M08).
- [ ] Bei großen/wachsenden DBs: `CLIO_DB_INITIAL_MB` vorbelegt (gegen Schreib-Latenzspitzen) und optional `CLIO_DB_COMPACT_ENABLED` für Online-Kompaktierung.
- [ ] `/metrics` gescrapt; Alarme auf Fehlerrate, Latenz, `clio_db_size_bytes` sowie Remap-Headroom (`clio_db_data_bytes` ↔ `clio_db_initial_bytes`).
- [ ] `/api/v1/info` als Health-/Deploy-Verifikation eingebunden.
- [ ] Backup-Strategie für die DB-Datei (konsistente Kopie, siehe M08).
- [ ] Optional: `CLIO_SIGNING_KEY` gesetzt und öffentlicher Schlüssel verteilt.
- [ ] Periodischer `verify`-Lauf zur Tamper-Evidence.

## Wichtige Betriebs-Eigenschaften (aus den ADRs)

- **Single-Instance**, kein Clustering — Verfügbarkeit hängt an einer Instanz
  ([ADR-002](../../../ARCHITECTURE.md#adr-002-single-instance-architektur-vorerst-kein-clustering)).
- **Crash-Recovery „gratis"** durch bbolts ACID-Transaktionen — kein separater
  Index-Rebuild ([ADR-006](../../../ARCHITECTURE.md#adr-006-append-only-storage-mit-in-memory-index)).
- **`compact` löscht keine Events** — nur Defragmentierung, atomarer Swap; per
  CLI offline oder via `CLIO_DB_COMPACT_ENABLED` online im Betrieb (kurze
  Downtime je Lauf) ([ADR-015](../../../ARCHITECTURE.md#adr-015-kompaktierung-defragmentiert-löscht-aber-keine-events)).
- **`/metrics` und `/docs` sind ohne Auth** — im Betrieb per Netz/Proxy
  absichern ([ADR-013](../../../ARCHITECTURE.md#adr-013-eigene-abhängigkeitsfreie-metriken-statt-prometheus-client)).

## Geschafft, wenn…

Du Clio in Docker mit persistentem Volume und Token ausrollen, eine
Prometheus-Alarmregel auf die HTTP-Fehlerrate schreiben, ein konsistentes
Backup ziehen und einen `compact`-Lauf sicher durchführen kannst.
