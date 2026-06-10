# clio

**cliostore** (Kurzform „Clio") — ein eigenständiger, in Go geschriebener
**Event Store**, funktional orientiert am Vorbild EventSourcingDB. Ein einzelnes,
abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt,
liest und live beobachtbar macht.

> Clio — die Muse der Geschichtsschreibung. Kurz, elegant, exakt das Thema.

Die vollständige Architektur, Roadmap und alle Entscheidungen stehen in
[`ARCHITECTURE.md`](./ARCHITECTURE.md).

## Status

**Stufe 0 (MVP) — funktional vollständig.** Lauffähig: `ping`, `write-events`
(atomar, monotone Event-IDs, `bbolt`-Storage) und `read-events` (NDJSON), alle
Datenrouten Bearer-Token-geschützt. Als Nächstes (Stufe 1): Preconditions
(Optimistic Concurrency) und `lowerBound`/`upperBound` beim Lesen.

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
```

## Tests

```bash
go test ./...
```
