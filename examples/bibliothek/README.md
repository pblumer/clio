# Beispiele: Bibliothek 📚

Lauffähige `curl`-Skripte für den **Hauptfaden** des Learning Path. Domäne:
Bücher unter `/books/...` mit dem Lebenszyklus `acquired → borrowed → returned
→ retired` (siehe
[Grundlagen 3](../../docs/learning-path/00-grundlagen/03-beispiel-bibliothek.md)).

## Voraussetzungen

1. Clio läuft lokal (siehe
   [Quickstart](../../docs/learning-path/00-grundlagen/02-clio-quickstart.md)
   oder `./00-start-server.sh`).
2. Token gesetzt — **identisch** zum `CLIO_API_TOKEN` des Servers:
   ```bash
   export TOKEN=dein-geheimes-token
   ```
3. Optional eine andere Basis-URL: `export CLIO_BASE=http://127.0.0.1:3000`

## Skripte (in dieser Reihenfolge)

| Skript | Modul | Zeigt |
|---|---|---|
| `00-start-server.sh` | Quickstart | Baut & startet Clio mit Token |
| `01-events-schreiben.sh` | [M01](../../docs/learning-path/module/M01-erstes-event.md) | Einzelnes & atomares Mehrfach-Schreiben |
| `02-lesen-und-filtern.sh` | [M02](../../docs/learning-path/module/M02-lesen-und-filtern.md) | recursive, Bounds, Typ-Filter, GET-Route |
| `03-observe.sh` | [M03](../../docs/learning-path/module/M03-live-observe.md) | Live-Streaming mit Hintergrund-Observer |
| `04-preconditions.sh` | [M04](../../docs/learning-path/module/M04-optimistic-concurrency.md) | Preconditions & 409 (Kurzvariante) |
| `05-schema-registrieren.sh` | [M05](../../docs/learning-path/module/M05-schemas.md) | Schema registrieren, gültig/ungültig (400) |
| `06-cel-query.sh` | [M06](../../docs/learning-path/module/M06-cel-queries.md) | run-query mit CEL-Prädikat |
| `07-verify-und-public-key.sh` | [M07](../../docs/learning-path/module/M07-integritaet-und-signaturen.md) | Hash-Kette prüfen, public-key |

## Ausführen

```bash
export TOKEN=dein-geheimes-token
examples/bibliothek/01-events-schreiben.sh
examples/bibliothek/02-lesen-und-filtern.sh
# ...
```

> Die Skripte schreiben echte Events in deine DB. Für einen sauberen Durchlauf
> kannst du vorher eine frische DB nutzen (`CLIO_DB_PATH=demo.db` beim Start).

Für fortgeschrittene Concurrency-/Invarianten-Beispiele siehe
[`../bankkonto/`](../bankkonto/).
