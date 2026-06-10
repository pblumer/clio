# clio

**cliostore** (Kurzform „Clio") — ein eigenständiger, in Go geschriebener
**Event Store**, funktional orientiert am Vorbild EventSourcingDB. Ein einzelnes,
abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt,
liest und live beobachtbar macht.

> Clio — die Muse der Geschichtsschreibung. Kurz, elegant, exakt das Thema.

Die vollständige Architektur, Roadmap und alle Entscheidungen stehen in
[`ARCHITECTURE.md`](./ARCHITECTURE.md).

## Status

**Stufe 0 + 1 — abgeschlossen.** Lauffähig: `ping`, `write-events` (atomar,
monotone Event-IDs, `bbolt`-Storage, **Preconditions** für Optimistic
Concurrency) und `read-events` (NDJSON, optionale **`lowerBound`/`upperBound`**),
alle Datenrouten Bearer-Token-geschützt. Als Nächstes (Stufe 2): `observe-events`
(Live-Streaming).

## Bauen & Starten

Voraussetzung: Go ≥ 1.24.

```bash
# Bauen
go build -o cliostore ./cmd/cliostore

# Starten (API-Token ist Pflicht)
CLIO_API_TOKEN=dein-geheimes-token ./cliostore

# Erreichbarkeit prüfen
curl http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```

### Konfiguration

| Variable          | Pflicht | Default    | Bedeutung                          |
|-------------------|---------|------------|------------------------------------|
| `CLIO_API_TOKEN`  | ja      | —          | Bearer-Token für geschützte Routen |
| `CLIO_ADDR`       | nein    | `:3000`    | Listen-Adresse des HTTP-Servers    |
| `CLIO_DB_PATH`    | nein    | `clio.db`  | Pfad zur bbolt-Datenbankdatei      |

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

## Tests

```bash
go test ./...
```
