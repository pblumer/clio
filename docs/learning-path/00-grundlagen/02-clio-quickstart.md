# Grundlagen 2 — Clio Quickstart

> **Für alle Rollen.** Ziel: in ~5 Minuten von Null zu einem geschriebenen und
> gelesenen Event.

## Lernziele

Nach diesem Modul kannst du:

- Clio installieren (fertiges Binary **oder** selbst bauen) und mit einem
  API-Token starten,
- die Erreichbarkeit prüfen (`ping`),
- dein erstes Event schreiben und wieder lesen,
- die interaktive API-Doku (Swagger UI) öffnen.

## Voraussetzungen

- Ein Terminal.
- Für die Hands-on-Teile: `curl` (Linux/macOS) **oder** PowerShell (Windows).
- **Nur wenn du selbst baust** (Variante B unten): **Go ≥ 1.24**.

> **Windows-Nutzer:innen:** Die Beispiele gibt es in zwei Varianten —
> Bash/`curl` (`.sh`) und **native PowerShell** (`.ps1`). Unten stehen beide
> Wege nebeneinander. Die `.ps1`-Skripte laufen mit Windows PowerShell 5.1
> (vorinstalliert) und PowerShell 7+ und brauchen kein `curl`.

## Schritt 1 — Clio holen

Du brauchst Clio als ausführbares `cliostore`(`.exe`). Wähle **einen** Weg:

### Variante A — Fertiges Binary herunterladen (empfohlen, kein Go nötig)

Auf der [**Releases-Seite**](https://github.com/pblumer/clio/releases/latest)
liegt für jede Plattform ein Archiv (`…_linux_amd64.tar.gz`,
`…_darwin_arm64.tar.gz`, `…_windows_amd64.zip` …) plus eine `checksums.txt`.
Lade das passende, entpacke es — fertig.

**Linux/macOS** (Beispiel Linux/amd64; für Apple Silicon `darwin_arm64` wählen):
```bash
VERSION=v0.1.0
BASE=https://github.com/pblumer/clio/releases/download/$VERSION
curl -sSL -O $BASE/cliostore_${VERSION}_linux_amd64.tar.gz
curl -sSL -O $BASE/checksums.txt
sha256sum --check --ignore-missing checksums.txt   # Integrität prüfen -> OK
tar -xzf cliostore_${VERSION}_linux_amd64.tar.gz
cd cliostore_${VERSION}_linux_amd64
./cliostore -version                               # -> cliostore v0.1.0
```

**Windows (PowerShell):**
```powershell
$VERSION = 'v0.1.0'
$base = "https://github.com/pblumer/clio/releases/download/$VERSION"
Invoke-WebRequest "$base/cliostore_${VERSION}_windows_amd64.zip" -OutFile clio.zip
Expand-Archive clio.zip -DestinationPath .
cd "cliostore_${VERSION}_windows_amd64"
.\cliostore.exe -version                           # -> cliostore v0.1.0
```

> Lieber Docker? Dann brauchst du gar nichts herunterzuladen:
> `docker run --rm -p 3000:3000 -e CLIO_API_TOKEN=dein-token ghcr.io/pblumer/clio:latest`
> — siehe [Docker in der README](../../../README.md#docker). Schritt 2 entfällt
> dann (das Token gibst du direkt mit).

### Variante B — Selbst bauen (für Contributor:innen, braucht Go ≥ 1.24)

**Linux/macOS:**
```bash
# Im Repo-Wurzelverzeichnis
make build            # erzeugt ./cliostore
./cliostore -version  # -> cliostore <version>
```

**Windows (PowerShell):**
```powershell
# Im Repo-Wurzelverzeichnis
go build -o cliostore.exe ./cmd/cliostore
.\cliostore.exe -version   # -> cliostore <version>
```

## Schritt 2 — Starten

Clio braucht **zwingend** ein API-Token. Wähle ein beliebiges Geheimnis:

**Linux/macOS:**
```bash
export TOKEN=dein-geheimes-token
CLIO_API_TOKEN=$TOKEN ./cliostore
```

**Windows (PowerShell):**
```powershell
$env:TOKEN = 'dein-geheimes-token'
$env:CLIO_API_TOKEN = $env:TOKEN
.\cliostore.exe
```

Du siehst eine Log-Zeile wie `cliostore lauscht addr=:3000`. Lass diesen
Prozess laufen und öffne ein **zweites Terminal** für die nächsten Schritte.

> Standardmäßig lauscht Clio auf `:3000` und legt die Datenbank als `clio.db`
> im Arbeitsverzeichnis an. Beides ist über Env-Variablen konfigurierbar —
> siehe [Konfiguration in der README](../../../README.md#konfiguration).

## Schritt 3 — Erreichbarkeit prüfen

`ping` ist die einzige Route ohne Auth:

**Linux/macOS:**
```bash
curl http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```
**Windows (PowerShell):**
```powershell
Invoke-RestMethod http://127.0.0.1:3000/api/v1/ping
# -> status: ok
```

## Schritt 4 — Erstes Event schreiben

Alle Datenrouten brauchen den Bearer-Header. Wir schreiben in den Stream
`/books/42` (das wird unser durchgehendes Beispiel):

**Linux/macOS:**
```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/42","type":"acquired","data":{"title":"Dune"}}]}'
```
**Windows (PowerShell):**
```powershell
$headers = @{ Authorization = "Bearer $($env:TOKEN)" }
$body = '{"events":[{"source":"library","subject":"/books/42","type":"acquired","data":{"title":"Dune"}}]}'
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:3000/api/v1/write-events `
  -Headers $headers -Body $body -ContentType 'application/json'
```

Der Server ergänzt `id`, `time` und `specversion` selbst und antwortet mit dem
gespeicherten Event (inkl. `hash` der Tamper-Evidence-Kette).

## Schritt 5 — Event lesen

**Linux/macOS:**
```bash
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42"}'
```
**Windows (PowerShell):**
```powershell
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:3000/api/v1/read-events `
  -Headers $headers -Body '{"subject":"/books/42"}' -ContentType 'application/json'
```

> Bequemer für ganze Lerneinheiten: die fertigen Skripte unter
> [`examples/bibliothek/`](../../../examples/bibliothek/) — `.sh` für
> Linux/macOS, `.ps1` für Windows.

Du bekommst dein Event als **NDJSON** zurück (ein JSON-Objekt pro Zeile).
Glückwunsch — du hast Event Sourcing in Aktion gesehen. 🎉

## Schritt 6 — Interaktive Doku

Clio liefert seine eigene Doku aus (ins Binary eingebettet, kein Internet
nötig):

- **Swagger UI:** <http://127.0.0.1:3000/docs> — „Authorize" mit deinem Token,
  dann „Try it out".
- **OpenAPI-Spec:** <http://127.0.0.1:3000/openapi.yaml>

## Schritt 7 — Status der Instanz

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/info
```

Liefert u. a. `version`, `uptimeSeconds`, `eventsTotal` und `syncMode` —
praktisch zum Verifizieren eines Deployments.

## Checkpoint

1. Warum funktioniert `read-events` ohne den `Authorization`-Header **nicht**,
   `ping` aber schon?
2. Welche drei Felder ergänzt der Server beim Schreiben automatisch?
3. Schreibe ein zweites Event in `/books/42` mit `type` `borrowed` und lies den
   Stream erneut. In welcher Reihenfolge kommen die Events zurück?

→ Lösungen in [`uebungen/loesungen.md`](../uebungen/loesungen.md#grundlagen-2).

---

**Weiter:** [Grundlagen 3 — Das Beispiel: Bibliothek](03-beispiel-bibliothek.md)
oder direkt zu deinem [Rollen-Track](../README.md#wähle-deine-rolle).
