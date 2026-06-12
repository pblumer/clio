# Beispiele: Bibliothek 📚

Lauffähige `curl`-Skripte für den **Hauptfaden** des Learning Path. Domäne:
Bücher unter `/books/...` mit dem Lebenszyklus `acquired → borrowed → returned
→ retired` (siehe
[Grundlagen 3](../../docs/learning-path/00-grundlagen/03-beispiel-bibliothek.md)).

Es gibt jedes Skript in **zwei Varianten** — wähle die deiner Plattform:

- **`.sh`** für Linux/macOS (Bash + `curl`)
- **`.ps1`** für Windows (PowerShell, kompatibel mit Windows PowerShell 5.1 und
  PowerShell 7+; nutzt native Cmdlets, **kein** curl nötig)

## Voraussetzungen

1. Clio läuft lokal (siehe
   [Quickstart](../../docs/learning-path/00-grundlagen/02-clio-quickstart.md)
   oder `00-start-server.sh` / `00-start-server.ps1`).
2. Token gesetzt — **identisch** zum `CLIO_API_TOKEN` des Servers:

   **Linux/macOS (Bash):**
   ```bash
   export TOKEN=dein-geheimes-token
   ```
   **Windows (PowerShell):**
   ```powershell
   $env:TOKEN = 'dein-geheimes-token'
   ```
3. Optional eine andere Basis-URL: `CLIO_BASE` (Default `http://127.0.0.1:3000`).

## Skripte (in dieser Reihenfolge)

| Skript (`.sh` / `.ps1`) | Modul | Zeigt |
|---|---|---|
| `00-start-server` | Quickstart | Baut & startet Clio mit Token |
| `01-events-schreiben` | [M01](../../docs/learning-path/module/M01-erstes-event.md) | Einzelnes & atomares Mehrfach-Schreiben |
| `02-lesen-und-filtern` | [M02](../../docs/learning-path/module/M02-lesen-und-filtern.md) | recursive, Bounds, Typ-Filter, GET-Route |
| `03-observe` | [M03](../../docs/learning-path/module/M03-live-observe.md) | Live-Streaming mit Hintergrund-Observer |
| `04-preconditions` | [M04](../../docs/learning-path/module/M04-optimistic-concurrency.md) | Preconditions & 409 (Kurzvariante) |
| `05-schema-registrieren` | [M05](../../docs/learning-path/module/M05-schemas.md) | Schema registrieren, gültig/ungültig (400) |
| `06-cel-query` | [M06](../../docs/learning-path/module/M06-cel-queries.md) | run-query mit CEL-Prädikat |
| `07-verify-und-public-key` | [M07](../../docs/learning-path/module/M07-integritaet-und-signaturen.md) | Hash-Kette prüfen, public-key |

## Ausführen

**Linux/macOS (Bash):**
```bash
export TOKEN=dein-geheimes-token
examples/bibliothek/01-events-schreiben.sh
examples/bibliothek/02-lesen-und-filtern.sh
# ...
```

**Windows (PowerShell):**
```powershell
$env:TOKEN = 'dein-geheimes-token'
.\examples\bibliothek\01-events-schreiben.ps1
.\examples\bibliothek\02-lesen-und-filtern.ps1
# ...
```

> Falls PowerShell die Skriptausführung blockiert (ExecutionPolicy), hilft für
> die aktuelle Sitzung:
> `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass`

> Die Skripte schreiben echte Events in deine DB. Für einen sauberen Durchlauf
> kannst du vorher eine frische DB nutzen (`CLIO_DB_PATH=demo.db` beim Start).

Für fortgeschrittene Concurrency-/Invarianten-Beispiele siehe
[`../bankkonto/`](../bankkonto/).
