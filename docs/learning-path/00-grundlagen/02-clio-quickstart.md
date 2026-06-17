# Grundlagen 2 — Clio Quickstart

> **Für alle Rollen.** Ziel: in ~5 Minuten von Null zu einem geschriebenen und
> gelesenen Event.

## Lernziele

Nach diesem Modul kannst du:

- Clio bauen und mit einem initialen Admin-Key starten,
- die Erreichbarkeit prüfen (`ping`),
- dein erstes Event schreiben und wieder lesen,
- die interaktive API-Doku (Swagger UI) öffnen.

## Voraussetzungen

- **Go ≥ 1.24** und ein Terminal.
- Für die Hands-on-Teile: `curl` (Linux/macOS) **oder** PowerShell (Windows).

> **Windows-Nutzer:innen:** Die Beispiele gibt es in zwei Varianten —
> Bash/`curl` (`.sh`) und **native PowerShell** (`.ps1`). Unten stehen beide
> Wege nebeneinander. Die `.ps1`-Skripte laufen mit Windows PowerShell 5.1
> (vorinstalliert) und PowerShell 7+ und brauchen kein `curl`.

## Schritt 1 — Bauen

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

Bei leerem Schlüsselbund (frische DB) braucht Clio **zwingend** ein Geheimnis,
aus dem es einen initialen **Admin-Key** bootet (ADR-025). Wähle ein beliebiges
Geheimnis und setze es als `CLIO_BOOTSTRAP_ADMIN_KEY`:

**Linux/macOS:**
```bash
export SECRET=dein-geheimnis
CLIO_BOOTSTRAP_ADMIN_KEY=$SECRET ./cliostore
```

**Windows (PowerShell):**
```powershell
$env:SECRET = 'dein-geheimnis'
$env:CLIO_BOOTSTRAP_ADMIN_KEY = $env:SECRET
.\cliostore.exe
```

Du siehst eine Log-Zeile wie `cliostore lauscht addr=:3000` und beim Bootstrap
zusätzlich den generierten **`kid`** des Admin-Keys. Der vollständige Token auf
der Leitung ist `kid.secret` — also der geloggte `kid` zusammen mit deinem
Geheimnis. Setze ihn fürs zweite Terminal:

**Linux/macOS:**
```bash
export TOKEN=<geloggter-kid>.$SECRET   # z. B. kid_ab12cd34.dein-geheimnis
```
**Windows (PowerShell):**
```powershell
$env:TOKEN = "<geloggter-kid>.$($env:SECRET)"
```

Lass den Server-Prozess laufen und öffne ein **zweites Terminal** für die
nächsten Schritte.

> **Hinweis:** `CLIO_API_TOKEN` existiert noch als **deprecated** Bootstrap-Pfad
> (bootet einen `legacy-token`-Admin-Key). Der Leitungswert ist auch dann
> `kid.secret`; ein altes `Bearer <token>` ohne `kid`-Präfix wird nicht mehr
> akzeptiert. Für neue Setups `CLIO_BOOTSTRAP_ADMIN_KEY` nutzen. Weitere Keys
> mit eigenen Scopes legst du zur Laufzeit über `POST /api/v1/keys` an — siehe
> [README — API-Keys](../../../README.md#api-keys-scopes--widerruf).

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
