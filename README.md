# clio

**cliostore** (Kurzform „Clio") — ein eigenständiger, in Go geschriebener
**Event Store**, funktional orientiert am Vorbild EventSourcingDB. Ein einzelnes,
abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt,
liest und live beobachtbar macht.

> Clio — die Muse der Geschichtsschreibung. Kurz, elegant, exakt das Thema.

Die vollständige Architektur, Roadmap und alle Entscheidungen stehen in
[`ARCHITECTURE.md`](./ARCHITECTURE.md).

## Status

**Stufe 0 (MVP) — in Arbeit.** Aktuell lauffähig: HTTP-Server mit
`GET/POST /api/v1/ping`, Config über Umgebungsvariablen und Bearer-Token-Auth.
Als Nächstes: `write-events` / `read-events` mit `bbolt`-Storage.

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

| Variable          | Pflicht | Default  | Bedeutung                          |
|-------------------|---------|----------|------------------------------------|
| `CLIO_API_TOKEN`  | ja      | —        | Bearer-Token für geschützte Routen |
| `CLIO_ADDR`       | nein    | `:3000`  | Listen-Adresse des HTTP-Servers    |

## Tests

```bash
go test ./...
```
