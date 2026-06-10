# clio

**cliostore** (Kurzform „Clio") — ein eigenständiger, in Go geschriebener
**Event Store**, funktional orientiert am Vorbild EventSourcingDB. Ein einzelnes,
abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt,
liest und live beobachtbar macht.

> Clio — die Muse der Geschichtsschreibung. Kurz, elegant, exakt das Thema.

Die vollständige Architektur, Roadmap und alle Entscheidungen stehen in
[`ARCHITECTURE.md`](./ARCHITECTURE.md).

## Status

**Stufe 0–2 — abgeschlossen.** Lauffähig: `ping`, `write-events` (atomar,
monotone Event-IDs, `bbolt`-Storage, **Preconditions** für Optimistic
Concurrency), `read-events` (NDJSON, optionale **`lowerBound`/`upperBound`**,
**`recursive`**) und **`observe-events`** (Live-Streaming: erst History, dann
offene Verbindung). Alle Datenrouten Bearer-Token-geschützt.

**Stufe 3 in Arbeit:** **Group Commit** als Default-Schreibstrategie (hoher
Durchsatz bei voller Durability, umschaltbar via `CLIO_SYNC`) — siehe
[Performance](#performance--durability) — sowie **Distribution**: statische
Single-Binaries für alle Plattformen (`make dist`), Docker-Image und
Release-Workflow. Offen: Kompaktierung, Metrics/Observability.

## Bauen & Starten

Voraussetzung: Go ≥ 1.24.

```bash
# Bauen (mit eingebetteter Version)
make build            # -> ./cliostore
./cliostore -version  # -> cliostore <version>

# oder direkt
go build -o cliostore ./cmd/cliostore

# Starten (API-Token ist Pflicht)
CLIO_API_TOKEN=dein-geheimes-token ./cliostore

# Erreichbarkeit prüfen
curl http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```

### Single-Binaries für alle Plattformen

```bash
make dist   # statische Binaries nach dist/ (linux/darwin/windows × amd64/arm64)
```

Bei einem Git-Tag `vX.Y.Z` baut der Release-Workflow diese Binaries automatisch
und hängt sie an ein GitHub-Release.

### Docker

```bash
make docker                       # Image cliostore:<version> bauen
docker run --rm -p 3000:3000 \
  -e CLIO_API_TOKEN=dein-token \
  -v clio-data:/data \
  cliostore:latest
```

Das Image basiert auf `distroless/static` (kein Shell, nonroot-User, statisches
Binary). Die Datenbank liegt unter `/data` (Volume mounten, um Daten zu
persistieren).

### Konfiguration

| Variable          | Pflicht | Default    | Bedeutung                          |
|-------------------|---------|------------|------------------------------------|
| `CLIO_API_TOKEN`  | ja      | —          | Bearer-Token für geschützte Routen |
| `CLIO_ADDR`       | nein    | `:3000`    | Listen-Adresse des HTTP-Servers    |
| `CLIO_DB_PATH`    | nein    | `clio.db`  | Pfad zur bbolt-Datenbankdatei      |
| `CLIO_SYNC`       | nein    | `group`    | Schreibstrategie: `group`/`always`/`off` (siehe Performance) |

### Events schreiben & lesen

```bash
TOKEN=dein-geheimes-token

# Ein oder mehrere Events atomar schreiben (id/time/specversion ergänzt der Server)
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"lib","subject":"/books/42","type":"acquired","data":{"title":"Dune"}}]}'

# Alle Events eines Subjects als NDJSON lesen
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42"}'

# Nur einen ID-Bereich lesen (beide Grenzen inklusive)
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42","lowerBound":"2","upperBound":"10"}'

# Rekursiv alle Events unterhalb von /books lesen
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true}'

# Nach Event-Typ(en) filtern (z. B. „alle Bestellungen")
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/orders","recursive":true,"types":["order-placed","order-cancelled"]}'
```

Der optionale `types`-Filter ist mit `recursive` und `lowerBound`/`upperBound`
kombinierbar und gilt ebenso für `observe-events`. Leer/weggelassen = alle Typen.

### Events live beobachten

`observe-events` liefert zuerst die passende History und hält die Verbindung
dann offen, um neue Events sofort als NDJSON nachzuliefern. Nach einem
Verbindungsabbruch verbindet man sich mit `lowerBound` neu und lädt so die
verpassten Events nach.

```bash
# Live alle Events unterhalb von /books beobachten (-N = ungepuffert)
curl -N -X POST http://127.0.0.1:3000/api/v1/observe-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true}'

# Reconnect ab einer bekannten Event-ID (verpasste Events nachholen)
curl -N -X POST http://127.0.0.1:3000/api/v1/observe-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books","recursive":true,"lowerBound":"42"}'
```

### Optimistic Concurrency (Preconditions)

`write-events` akzeptiert optionale Preconditions, die **atomar** mit dem Write
geprüft werden. Schlägt eine fehl, wird nichts geschrieben und der Server
antwortet mit **HTTP 409**.

```bash
# Nur schreiben, wenn der Stream noch leer ist
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"lib","subject":"/books/42","type":"acquired"}],
        "preconditions":[{"type":"isSubjectPristine","payload":{"subject":"/books/42"}}]
      }'

# Nur schreiben, wenn das letzte Event des Streams diese ID hat
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"lib","subject":"/books/42","type":"borrowed"}],
        "preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/books/42","eventId":"7"}}]
      }'
```

## Performance & Durability

Writes laufen standardmäßig über **Group Commit** (`CLIO_SYNC=group`): viele
gleichzeitige Schreibvorgänge teilen sich ein `fsync`. Das liefert unter Last
hohen Durchsatz **bei voller Durability**. Die Strategie ist umschaltbar:

| `CLIO_SYNC` | fsync | Stärke | Schwäche |
|---|---|---|---|
| `group` (Default) | pro Batch | hoher Durchsatz unter Last, voll durable | höhere Latenz bei einzelnen, sequentiellen Writes |
| `always` | pro Write | geringste Einzel-Latenz, voll durable | begrenzter Durchsatz |
| `off` | nie | maximaler Durchsatz | Crash kann zuletzt geschriebene Events verlieren |

Richtwerte aus den enthaltenen Benchmarks bei ~256 gleichzeitigen Schreibern
(SSD; absolute Zahlen hardwareabhängig): `group` ≈ **31×** Durchsatz von
`always` und nahe an `off` — also fast die Geschwindigkeit ohne fsync, aber
crash-sicher.

```bash
# Benchmarks selbst ausführen
go test -run='^$' -bench=BenchmarkAppend -benchmem ./internal/store/
```

## Tests

```bash
go test ./...
go test -race ./...   # Nebenläufigkeit (Observe, Group Commit)
```
