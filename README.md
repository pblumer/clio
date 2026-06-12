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
offene Verbindung) — wahlweise auch bequem per **`GET /api/v1/events/<subject>`**.
Alle Datenrouten Bearer-Token-geschützt.

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

### Interaktive API-Doku (OpenAPI / Swagger UI)

Die laufende Instanz liefert ihre eigene Dokumentation aus — alles ins Binary
eingebettet, kein Internet nötig:

- **`http://127.0.0.1:3000/docs`** — Swagger UI zum interaktiven Ausprobieren
  („Authorize" mit dem Bearer-Token, dann „Try it out").
- **`http://127.0.0.1:3000/openapi.yaml`** — die OpenAPI-3-Spezifikation zum
  Import in eigene Tools (Postman, Insomnia, Codegen).

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
| `CLIO_SIGNING_KEY`| nein    | —          | base64-Ed25519-Schlüssel; aktiviert Event-Signaturen        |

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

### Bequemer lesen per GET-Pfad

Für `curl`/Tools gibt es eine schreibgeschützte Komfortroute, bei der das Subject
direkt im Pfad steht (Optionen als Query-Parameter):

```bash
# Events eines Streams
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/events/books/42

# Eltern-Pfad: liefert automatisch alles darunter (recursive Default true)
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/events/books

# Wurzel: alle Events
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/events

# Mit Optionen: Typ-Filter (wiederholbar), Bounds, recursive abschalten
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/events/orders?type=order-placed&type=order-cancelled&lowerBound=10"

# Live beobachten (wie observe-events)
curl -N -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/events/books?watch=true"
```

Query-Parameter: `recursive` (Default `true`), `lowerBound`, `upperBound`,
`type` (wiederholbar), `watch=true`. Auth läuft weiter über den Bearer-Header.

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

# Nur schreiben, wenn die CEL-Abfrage über den Scope kein Treffer-Event liefert
# (z. B. ein Konto nur einmal eröffnen)
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"bank","subject":"/accounts/42","type":"opened"}],
        "preconditions":[{"type":"isQueryResultEmpty",
          "payload":{"subject":"/accounts/42","where":"event.type == '\''opened'\''"}}]
      }'
```

Query-Preconditions (`isQueryResultEmpty`/`isQueryResultNonEmpty`) prüfen eine
CEL-Bedingung über den Scope und sind das `isEventQlQueryTrue`-Äquivalent.

## Unveränderlichkeit & Tamper-Evidence

Jedes Event wird über eine **SHA-256-Hash-Kette** mit seinem Vorgänger
verknüpft (`predecessorhash` → `hash`, Genesis = 64 Nullen). Damit ist jede
nachträgliche Änderung an der Historie **kryptografisch nachweisbar** — nicht
nur durch die append-only-API verhindert.

```bash
# Integrität der gesamten Kette prüfen
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/verify
# -> {"ok":true,"count":123,"head":"<hash>"}
# Bei Manipulation: {"ok":false,"brokenAt":"<id>","reason":"..."}
```

### Signaturen (Authentizität)

Optional signiert der Server jedes Event mit einem **Ed25519**-Schlüssel über
seinen Hash — das beweist zusätzlich die *Urheberschaft* (nicht nur Integrität).

```bash
# Schlüsselpaar erzeugen
./cliostore gen-key
# -> CLIO_SIGNING_KEY=<seed-base64>
#    # public key (zum Verifizieren): <public-base64>

# Server mit Signieren starten
CLIO_API_TOKEN=… CLIO_SIGNING_KEY=<seed-base64> ./cliostore

# Öffentlichen Schlüssel abrufen (Clients prüfen damit selbst)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/public-key
```

`verify` prüft dann auch die Signaturen mit. Ohne `CLIO_SIGNING_KEY` bleibt
`signature` `null` (abwärtskompatibel).

### Abfragen mit CEL (`run-query`)

Events lassen sich über ein **CEL-Prädikat** (`where`) filtern — über die
Variable `event` (Metadaten + `event.data`). Scope wie beim Lesen
(`subject`/`recursive`/Bounds), optionales `limit`.

```bash
curl -X POST http://127.0.0.1:3000/api/v1/run-query \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/orders","recursive":true,
       "where":"event.type == '\''placed'\'' && has(event.data.amount) && event.data.amount > 100"}'
```

`has(event.data.x)` schützt vor fehlenden Feldern; ein Auswertungsfehler eines
Events gilt als „kein Treffer".

### Verfügbare Event-Typen

```bash
# Alle bisher geschriebenen Typen (mit Anzahl), als NDJSON
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/read-event-types
# -> {"type":"acquired","count":2}
#    {"type":"borrowed","count":1}
```

### Event-Schemas

Pro Event-Typ lässt sich ein **JSON Schema** registrieren; danach wird `data`
beim Schreiben dagegen validiert (Verstoß → 400). Schemas sind unveränderlich,
und eine Registrierung gelingt nur, wenn die bestehende Historie des Typs konform
ist.

```bash
# Schema registrieren
curl -X POST http://127.0.0.1:3000/api/v1/register-event-schema \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"type":"order-placed","schema":{"type":"object","required":["amount"],
       "properties":{"amount":{"type":"number"}}}}'

# Schema lesen
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/read-event-schema?type=order-placed"
```

`read-event-types` zeigt pro Typ zusätzlich `hasSchema`.

## Observability

Jede Anfrage wird strukturiert geloggt (Methode, Route, Status, Dauer). Unter
**`/metrics`** liegen Prometheus-Metriken — ohne externe Client-Bibliothek:

```bash
curl http://127.0.0.1:3000/metrics
```

Enthalten u. a.: `clio_http_requests_total{method,route,status}`,
`clio_http_request_duration_seconds` (Histogramm), `clio_events_written_total`,
`clio_precondition_failures_total`, `clio_active_observers`, `clio_events_total`,
`clio_db_size_bytes`.

### Wartung: Kompaktierung

Die Datenbank wächst monoton (Events sind unveränderlich). `compact`
defragmentiert die bbolt-Datei **offline** (atomarer Swap), ohne Events zu
löschen oder zu verändern — die Hash-Kette bleibt gültig:

```bash
# Server vorher stoppen (der Befehl scheitert sonst am Datei-Lock)
CLIO_DB_PATH=clio.db ./cliostore compact
# -> kompaktiert: clio.db — 2097152 -> 1048576 bytes (50.0% kleiner)
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
