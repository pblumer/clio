# M08 — Betrieb & Durability

> **Tracks:** Betrieb · **Dauer:** ~30 Min

## Lernziele

- Clio als **Binary** und als **Docker-Container** mit persistentem Volume
  ausrollen.
- Alle `CLIO_*`-Variablen kennen und die richtige **`CLIO_SYNC`**-Strategie
  wählen.
- Ein **Backup** ziehen und die DB-Datei mit `compact` defragmentieren.

## Voraussetzungen

- [Grundlagen 2 — Quickstart](../00-grundlagen/02-clio-quickstart.md). Docker
  für den Container-Teil.

## Inhalt

### Konfiguration (Env-Variablen)

| Variable | Pflicht | Default | Bedeutung |
|---|---|---|---|
| `CLIO_BOOTSTRAP_ADMIN_KEY` | nein* | — | Geheimnis, aus dem **bei leerem Schlüsselbund** ein initialer Admin-Key gebootet wird (ADR-025). Der `kid` wird beim Start geloggt; der Leitungswert ist `kid.secret`. |
| `CLIO_API_TOKEN` | nein* | — | **Deprecated** (ADR-008 → ADR-025): bootet bei leerem Bund einen `legacy-token`-Admin-Key. Leitungswert ist danach ebenfalls `kid.secret`. |
| `CLIO_ADDR` | nein | `:3000` | Listen-Adresse |
| `CLIO_DB_PATH` | nein | `clio.db` | Pfad zur bbolt-Datei |
| `CLIO_DB_INITIAL_MB` | nein | `0` (aus) | Vorab-Dimensionierung der Mmap/Datei in MiB (z. B. `4096`); gegen Latenzspitzen bei wachsender DB. Strikt grow-only. |
| `CLIO_DB_MONITOR_INTERVAL` | nein | `60s` | Intervall des Headroom-Monitors (warnt vor Erreichen der vorbelegten Grenze); `0` = aus. |
| `CLIO_DB_GROW_THRESHOLD_PCT` | nein | `80` | Warn-Schwelle des Monitors (% der vorbelegten Größe). |
| `CLIO_DB_COMPACT_ENABLED` | nein | `false` | Online-Hintergrund-Kompaktierung im laufenden Betrieb (kurze Downtime je Lauf). |
| `CLIO_DB_COMPACT_INTERVAL_H` | nein | `6` | Intervall der Hintergrund-Kompaktierung in Stunden. |
| `CLIO_SYNC` | nein | `group` | Schreibstrategie (`group`/`always`/`off`) |
| `CLIO_SIGNING_KEY` | nein | — | base64-Ed25519-Seed; aktiviert Signaturen |

\* Bei leerem Schlüsselbund (frische DB) muss **eines** von
`CLIO_BOOTSTRAP_ADMIN_KEY` oder `CLIO_API_TOKEN` gesetzt sein, sonst verweigert
der Server den Start. Existieren bereits Keys, ist beides optional.

Quelle: [README — Konfiguration](../../../README.md#konfiguration) ·
[README — API-Keys](../../../README.md#api-keys-scopes--widerruf).

### Deployment per Binary

```bash
make dist   # statische Binaries nach dist/ (linux/darwin/windows × amd64/arm64)
# Beim ersten Start einen initialen Admin-Key booten (Leitungswert: kid.secret).
CLIO_BOOTSTRAP_ADMIN_KEY=<secret> CLIO_DB_PATH=/var/lib/clio/clio.db ./cliostore
```

Verifiziere das Deployment über `/api/v1/info` (Version, Uptime, `eventsTotal`,
`syncMode`).

### Deployment per Docker

```bash
make docker                       # Image cliostore:<version>
docker run --rm -p 3000:3000 \
  -e CLIO_BOOTSTRAP_ADMIN_KEY=<secret> \
  -v clio-data:/data \
  cliostore:latest
# Beim ersten Start wird der kid geloggt; der Schlüssel ist dann kid.secret.
```

Das Image basiert auf `distroless/static` (kein Shell, nonroot, statisches
Binary). Die DB liegt unter `/data` — **Volume mounten**, sonst sind die Daten
beim Container-Neustart weg.

### Durability: CLIO_SYNC verstehen

Writes laufen standardmäßig über **Group Commit**: viele gleichzeitige Writes
teilen sich *ein* `fsync`
([ADR-009](../../../ARCHITECTURE.md#adr-009-group-commit-als-default-schreibstrategie)).

| `CLIO_SYNC` | fsync | Stärke | Schwäche |
|---|---|---|---|
| `group` (Default) | pro Batch | hoher Durchsatz unter Last, voll durable | höhere Latenz bei einzelnen, sequentiellen Writes |
| `always` | pro Write | geringste Einzel-Latenz, voll durable | begrenzter Durchsatz |
| `off` | nie | maximaler Durchsatz | Crash kann zuletzt geschriebene Events verlieren |

**Faustregel:** Last mit vielen parallelen Writes → `group`. Wenige,
sequentielle, latenzkritische Writes → `always`. `off` nur für unkritische
Bulk-Importe, bei denen Crash-Verlust akzeptabel ist.

Richtwert aus den Benchmarks (~256 parallele Schreiber, SSD): `group` ≈ **31×**
Durchsatz von `always` und nahe an `off` — fast die Geschwindigkeit ohne fsync,
aber crash-sicher.

### Crash-Recovery

Kein separater Index-Rebuild nötig: bbolts ACID-Transaktionen halten Events und
Index immer konsistent
([ADR-006](../../../ARCHITECTURE.md#adr-006-append-only-storage-mit-in-memory-index)).
Nach einem Crash startet Clio einfach wieder.

### Backup

Die DB ist eine einzelne bbolt-Datei. Für ein **konsistentes** Backup entweder
die Instanz kurz stoppen und die Datei kopieren, oder ein dateisystem-/
volume-konsistentes Snapshot-Verfahren nutzen. Ein periodischer `verify`-Lauf
([M07](M07-integritaet-und-signaturen.md)) bestätigt die Unverändertheit.

### Wartung: Kompaktierung

Die DB wächst monoton (Events sind unveränderlich). `compact` defragmentiert die
Datei **offline** (atomarer Swap), **ohne** Events zu löschen — die Hash-Kette
bleibt gültig
([ADR-015](../../../ARCHITECTURE.md#adr-015-kompaktierung-defragmentiert-löscht-aber-keine-events)).

```bash
# Server vorher stoppen (sonst scheitert der Befehl am Datei-Lock)
CLIO_DB_PATH=/var/lib/clio/clio.db ./cliostore compact
# -> kompaktiert: ... 2097152 -> 1048576 bytes (50.0% kleiner)
```

Die Größe ist als Metrik `clio_db_size_bytes` beobachtbar
([M09](M09-observability.md)).

**Online statt offline:** Mit `CLIO_DB_COMPACT_ENABLED=true` defragmentiert Clio
periodisch (`CLIO_DB_COMPACT_INTERVAL_H`, Default 6h) **im laufenden Betrieb** —
ohne den Server zu stoppen. Pro Lauf gibt es eine kurze Downtime, in der alle
Zugriffe blockieren, bis die DB geschlossen, defragmentiert und neu geöffnet ist.

### Wachstum & Latenz: Vorab-Dimensionierung

bbolt mappt die Datei beim Wachsen neu (Remap) und hält dabei kurz einen
exklusiven Lock — bei großen Datenbanken unter Leselast erzeugt das spürbare
**Schreib-Latenzspitzen**. `CLIO_DB_INITIAL_MB` dimensioniert die Mmap vorab
(z. B. `4096` für 4 GiB) und verschiebt diese Remaps weit nach hinten; der
Headroom-Monitor warnt rechtzeitig, falls der genutzte Umfang die vorbelegte
Grenze erreicht. Der **genutzte** Umfang (getrennt von der ggf. vorbelegten
Dateigröße) ist als `clio_db_data_bytes` beobachtbar, die Grenze als
`clio_db_initial_bytes`. Details: [Storage-Scaling-Plan](../../plans/storage-scaling-plan.md).

## Hands-on

1. Starte Clio in Docker mit Volume und Token, schreibe ein paar Events,
   **restarte** den Container und prüfe per `read-events`, dass die Events noch
   da sind.
2. Stoppe die Instanz und führe `compact` aus; vergleiche die Dateigröße.

## Checkpoint

1. Du hast einen Service mit *einzelnen, latenzkritischen* Writes. Welche
   `CLIO_SYNC`-Einstellung wählst du, und warum nicht den Default?
2. Warum scheitert `compact` absichtlich, wenn eine Instanz läuft?
3. Was passiert mit den Daten, wenn du den Docker-Container **ohne** Volume
   startest und neu startest?

→ [Lösungen](../uebungen/loesungen.md#m08)

---

**Weiter:** [M09 — Observability](M09-observability.md)
