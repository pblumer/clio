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
| `CLIO_API_TOKEN` | ja | — | Bearer-Token für geschützte Routen |
| `CLIO_ADDR` | nein | `:3000` | Listen-Adresse |
| `CLIO_DB_PATH` | nein | `clio.db` | Pfad zur bbolt-Datei |
| `CLIO_SYNC` | nein | `group` | Schreibstrategie (`group`/`always`/`off`) |
| `CLIO_SIGNING_KEY` | nein | — | base64-Ed25519-Seed; aktiviert Signaturen |

Quelle: [README — Konfiguration](../../../README.md#konfiguration).

### Deployment per Binary

```bash
make dist   # statische Binaries nach dist/ (linux/darwin/windows × amd64/arm64)
CLIO_API_TOKEN=<secret> CLIO_DB_PATH=/var/lib/clio/clio.db ./cliostore
```

Verifiziere das Deployment über `/api/v1/info` (Version, Uptime, `eventsTotal`,
`syncMode`).

### Deployment per Docker

```bash
make docker                       # Image cliostore:<version>
docker run --rm -p 3000:3000 \
  -e CLIO_API_TOKEN=<secret> \
  -v clio-data:/data \
  cliostore:latest
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
