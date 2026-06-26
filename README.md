# clio

**cliostore** (Kurzform „Clio") — ein eigenständiger, in Go geschriebener
**Event Store**, funktional orientiert am Vorbild EventSourcingDB. Ein einzelnes,
abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt,
liest und live beobachtbar macht.

> Clio — die Muse der Geschichtsschreibung. Kurz, elegant, exakt das Thema.

Die vollständige Architektur, Roadmap und alle Entscheidungen stehen in
[`ARCHITECTURE.md`](./ARCHITECTURE.md).

> **Neu hier?** Der [**Clio Learning Path**](./docs/learning-path/) führt dich
> rollenbasiert ein (Einsteiger:in, Anwendungsentwickler:in, Betrieb,
> Contributor, Architekt:in) — mit Modulen und lauffähigen Beispielen unter
> [`examples/`](./examples/).

> **Mit einem KI-Agenten unterwegs?** [`SESSION_PROMPT.md`](./SESSION_PROMPT.md)
> enthält einen wiederverwendbaren Prompt, der einen Agenten (Claude Code o. ä.)
> zu Beginn jeder Session auf die Prinzipien und Konventionen von Clio
> einnordet.

## 👁 Ein Blick ins Betriebs-Dashboard

![clio Betriebs-Dashboard — Eventstrom über die Zeit, Live-Events und Telemetrie unter /ui](docs/screenshots/dashboard.png)

> Alles eingebettet im **Single-Binary**, ganz ohne externe Dienste: ein
> **Eventstrom-Diagramm über die Zeit** (mit Maus-**Box-Zoom**), ein
> **Live-Event-Stream**, ein **Explorer**, eine **Query-Konsole** und
> **Live-Telemetrie** — erreichbar unter `http://127.0.0.1:3000/ui`.
>
> Sieben Tabs, kein Build-Step, kein CDN, kein Grafana nötig. Wie das alles im
> Detail funktioniert? 👉 [**Betriebs-Dashboard (`/ui`)**](#betriebs-dashboard-ui)

## Status

> **Für den Betrieb:** ist clio das Richtige für meinen Einsatz, und wie betreibe
> ich es sicher? → [`docs/production-readiness.md`](./docs/production-readiness.md)
> (Einsatzprofile, Garantien/Nicht-Garantien, Betriebsprofile) und
> [`docs/threat-model.md`](./docs/threat-model.md) (wogegen clio schützt — und
> wogegen nicht).

**🎉 v0.2.0 veröffentlicht** — aktuelles Release. Neu gegenüber v0.1.0 u. a.:
benannte **API-Keys mit Scopes, Widerruf und Audit** (ADR-025), transparente
**Wert-Kompression** der Event-Ablage (ADR-024), **skalierbare
Speicherverwaltung** (Pre-Sizing, Headroom-Monitor, Online-Kompaktierung) sowie
eine **Sekundär-Query auf `event.data`** mit internem Feld-Index (ADR-029).
Fertige Single-Binaries für alle Plattformen (Linux/macOS/Windows × amd64/arm64)
als Archive **inkl. SHA-256-Checksums** sowie ein Multi-Arch-Docker-Image liegen
unter [Releases](https://github.com/pblumer/clio/releases/latest) bzw. in der
GitHub Container Registry (`ghcr.io/pblumer/clio`). Siehe
[Installation](#single-binaries-für-alle-plattformen).

> **⚠️ Auth-Änderung in v0.2.0 (ADR-025):** Das alte `Bearer <token>` ohne
> `kid`-Präfix wird **nicht mehr akzeptiert**. `CLIO_API_TOKEN` ist deprecated
> und bootet bei leerem Schlüsselbund einen `legacy-token`-Admin-Key; der
> Leitungswert ist danach `kid.secret`. Neu bevorzugt: `CLIO_BOOTSTRAP_ADMIN_KEY`.

**Stufe 0–2 — abgeschlossen.** Lauffähig: `ping`, `write-events` (atomar,
monotone Event-IDs, `bbolt`-Storage, **Preconditions** für Optimistic
Concurrency), `read-events` (NDJSON, optionale **`lowerBound`/`upperBound`**,
**`recursive`**) und **`observe-events`** (Live-Streaming: erst History, dann
offene Verbindung) — wahlweise auch bequem per **`GET /api/v1/events/<subject>`**.
Alle Datenrouten sind durch benannte **API-Keys mit Scopes** geschützt (ADR-025,
Format `kid.secret`) — siehe [API-Keys](#api-keys-scopes--widerruf).

**Stufe 3 — abgeschlossen.** **Group Commit** als Default-Schreibstrategie (hoher
Durchsatz bei voller Durability, umschaltbar via `CLIO_SYNC`) — siehe
[Performance](#performance--durability) — sowie **Distribution**: statische
Single-Binaries (`make dist` / `make package`), Docker-Image und ein
tag-getriggerter Release-Workflow (GitHub-Release + GHCR). Kompaktierung
(`cliostore compact`) und Observability (`/metrics`) sind ebenfalls vorhanden.

## Bauen & Starten

Voraussetzung: Go ≥ 1.24.

```bash
# Bauen (mit eingebetteter Version)
make build            # -> ./cliostore
./cliostore -version  # -> cliostore <version>

# oder direkt
go build -o cliostore ./cmd/cliostore

# Starten — beim ersten Start einen initialen Admin-Key booten (ADR-025).
# Der vollständige Schlüssel ist dann kid.secret: der beim Start geloggte kid
# zusammen mit dem hier gesetzten Geheimnis (z. B. kid_ab12cd34.dein-geheimnis).
CLIO_BOOTSTRAP_ADMIN_KEY=dein-geheimnis ./cliostore

# Erreichbarkeit prüfen
curl http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```

> **Liveness/Readiness:** Für Container-/Orchestrator-Probes `GET /api/v1/ping`
> verwenden (auth-frei, ohne Store-Zugriff, eigene Goroutine je Request). So
> bleibt die Probe von langlaufenden Lese-/Query-Scans **entkoppelt** und schlägt
> nicht fälschlich an, nur weil gerade eine breite `run-query` läuft. Lese-/
> Query-Routen (`read-events`, `run-query`, `observe-events`) eignen sich nicht
> als Probe. RAM-Headroom einplanen: ein breiter Scan deserialisiert pro Event den
> Payload und hält eine bbolt-Lesetransaktion offen — unter hoher Schreiblast
> wächst der Speicher-/DB-Bedarf; ggf. `CLIO_QUERY_TIMEOUT` setzen.

### Interaktive API-Doku (OpenAPI / Swagger UI)

Die laufende Instanz liefert ihre eigene Dokumentation aus — alles ins Binary
eingebettet, kein Internet nötig:

- **`http://127.0.0.1:3000/docs`** — Swagger UI zum interaktiven Ausprobieren
  („Authorize" mit dem Bearer-Token, dann „Try it out").
- **`http://127.0.0.1:3000/openapi.yaml`** — die OpenAPI-3-Spezifikation zum
  Import in eigene Tools (Postman, Insomnia, Codegen).

### Betriebs-Dashboard (`/ui`)

Ein schlankes, ebenfalls eingebettetes Dashboard für Monitoring & Observing —
kein Prometheus/Grafana nötig, um „mal eben draufzuschauen". Optik im
**Sci-Fi-/HUD-Stil** (Sternenfeld, Neon-Glow), weiterhin Vanilla JS ohne
Build-Step/CDN:

![Betriebs-Dashboard: Eventstrom-Diagramm, Live-Events und Telemetrie](docs/screenshots/dashboard.png)

- **`http://127.0.0.1:3000/ui`** — Bearer-Token eingeben, **Verbinden**. Statische
  Seite (Vanilla JS, kein Build-Step) mit sieben Tabs:
  - **Dashboard** — ein **Eventstrom-Liniendiagramm über die Zeit**: gespeist aus
    `GET /api/v1/event-stats` (serverseitiges Histogramm der Eventmengen nach
    Event-Zeit — beim Start aus der **gesamten Historie** aufgebaut, danach live
    fortgeschrieben; **ohne** die Historie zu streamen), umschaltbar **Rate** je
    Zeitabschnitt bzw. **kumuliert** sowie **Lin/Log**-y-Achse (Log macht einen
    stetigen kleinen Strom neben hohen Spitzen sichtbar). Zudem umschaltbar
    **Gesamt** vs. **Pro Source** — letzteres schlüsselt den Strom nach
    CloudEvents-`source` auf (Top-Sources + „andere"), wahlweise **gestapelt**
    (Anteil am Strom) oder als **überlagerte Linien** (Vergleich), mit Farb-Legende
    (gespeist aus `GET /api/v1/event-stats?by=source`). Mit der Maus lässt sich ein **Bereich
    aufziehen** (zoomt auf Zeit- **und** Wertausschnitt — so wird auch ein
    wenige-aber-stetig-Fluss neben einer Spitze sichtbar); **Esc**, Doppelklick
    oder **Reset** stellt die Standardskala wieder her. Darunter ein **einklappbares
    Live-Events-Fenster**, das per `observe`-Stream (`lowerBound` = höchste ID + 1)
    **nur neue Events** ab dem Verbinden zeigt (neueste oben, aufklappbare `data`).
    Reißt ein Stream ab (Hintergrund-Tab-Drosselung, Broker-Drop, Netzwerk),
    **verbindet sich der Ticker automatisch neu** (Backoff, lückenloses Fortsetzen
    ab der zuletzt gesehenen ID) — gleiches gilt für den Live-Events-Tab.
    Dazu **Live-Telemetrie-Charts**
    (CPU-Last, Heap-Speicher, Event-Durchsatz, Request-Rate als glühende
    Sparklines) sowie Events total, DB-Größe (mit **Füllgrad-Balken**: belegt
    vs. wiederverwendbarer freier Platz), aktive Observer, Uptime und
    Latenz (p50/p99) mit wählbarem Auto-Refresh; liest `/api/v1/info` und
    `/metrics` derselben Instanz.
  - **Live-Events** — streamt `GET /api/v1/events/<subject>?watch=true`
    (erst History, dann live) mit Subject-/Typ-Filter, Pause/Fortsetzen und
    aufklappbarer `data` je Event.
  - **Explorer** — read-only: navigierbarer Subject-Baum
    (`read-subjects?tree=true`, Klick lädt die Events des Subjects),
    Event-Typen mit Anzahl und aufklappbaren Schemas (`read-event-types` /
    `read-event-schema`) sowie ein Integritäts-Panel (`verify` / `public-key`).
  - **Erzeugen** — Onboarding-Hilfe zum schnellen Befüllen: Formular zum
    **Events schreiben** (`write-events`) mit Vorlagen, Mehrfach-Erzeugung und
    **Ein-Klick-Beispiel-Szenarien** unter einem Prefix; optional **Schemas
    registrieren** (`register-event-schema`). Schreibt über dieselben
    token-geschützten Endpunkte wie die API — keine neue Privilegien-Ebene.
  - **Keys** — Verwaltung der **API-Keys** (ADR-025, Scope `admin`): Übersicht
    aller Keys (kid, Name, Scopes, Status, erstellt/widerrufen) mit Filter,
    **Anlegen** (das einmalige `kid.secret` wird mit Copy-Knopf und Einmal-Hinweis
    gezeigt) und **Widerruf** (mit Bestätigung und Self-Lockout-Warnung beim
    letzten aktiven Admin). Nutzt die `admin`-geschützten `/api/v1/keys*`-Routen.
  - **Query** — `run-query`-Konsole mit CEL-Editor: Syntax-Highlighting,
    kontextsensitive Autovervollständigung (inkl. aus echten Events gelernter
    `event.data.*`-Felder), <kbd>Ctrl/Cmd</kbd>+<kbd>Enter</kbd> zum Ausführen,
    Inline-Fehlermeldungen, Scope-/Projektions-Optionen (`recursive`, `limit`,
    `lowerBound`/`upperBound`, `select`). Dazu **Verlauf &amp; Favoriten**
    (persistent im Browser) und **Export** der Ergebnisse als NDJSON oder CSV.
  - **Hilfe** — CEL-Referenz (`event`-Felder, Operatoren/Funktionen,
    Projektion) plus Beispiele, die sich direkt in den Query-Editor laden lassen.

Scope & Roadmap stehen in
[`docs/web-ui-scope.md`](./docs/web-ui-scope.md) (ADR-020).

### Postman & Smoke-Test (Newman)

Quelle der Wahrheit ist die OpenAPI-Spec (`internal/apidocs/openapi.yaml`).
Daraus abgeleitet liegt unter `postman/` eine fertige Collection inkl.
Test-Skripten:

- `postman/clio.postman_collection.json` — alle Endpunkte, geordnet als
  Durchlauf (schreiben → lesen → abfragen → verifizieren) mit `pm.test`-Checks
  (Status-Codes, NDJSON/`problem+json`-Header, Hash-Ketten-Integrität).
- `postman/local.postman_environment.json` — `baseUrl` + `token`.

**In Postman:** beide Dateien importieren (oder direkt die laufende
`…/openapi.yaml` importieren, um die Endpunkte zu generieren), Environment
wählen, „Send".

**Headless / CI per Knopfdruck:**

```bash
make smoke   # baut das Binary, startet den Server auf :3999 (temp-DB),
             # führt die Collection per Newman aus, fährt sauber herunter
```

`make smoke` braucht nur `npx` (Node) — Newman wird bei Bedarf geholt. Port und
Token sind über `SMOKE_PORT` / `SMOKE_TOKEN` überschreibbar.

Nach Änderungen an der Spec lässt sich das Gerüst der Collection regenerieren
(die gepflegten `pm.test`-Skripte ergänzt man danach von Hand):

```bash
make postman-gen
```

### Single-Binaries für alle Plattformen

```bash
make dist      # statische Binaries nach dist/ (linux/darwin/windows × amd64/arm64)
make package   # zusätzlich als .tar.gz/.zip + checksums.txt verpacken
```

Bei einem Git-Tag `vX.Y.Z` baut der Release-Workflow die Binaries automatisch,
verpackt sie pro Plattform als Archiv (`.tar.gz` bzw. `.zip` für Windows, jeweils
inkl. `LICENSE`/`README.md`), erzeugt eine `checksums.txt` (SHA-256) und hängt
alles an ein GitHub-Release.

> **Windows Server?** Eine vollständige Schritt-für-Schritt-Anleitung
> (Installation, Betrieb als Dienst, Firewall, Reverse Proxy, Backup) steht unter
> [**docs/windows-server-2022.md**](./docs/windows-server-2022.md).

Aus einem Release installieren (Beispiel Linux/amd64):

```bash
VERSION=v0.2.0
curl -sSL -O https://github.com/pblumer/clio/releases/download/$VERSION/cliostore_${VERSION}_linux_amd64.tar.gz
curl -sSL -O https://github.com/pblumer/clio/releases/download/$VERSION/checksums.txt
sha256sum --check --ignore-missing checksums.txt   # Integrität prüfen
tar -xzf cliostore_${VERSION}_linux_amd64.tar.gz
./cliostore_${VERSION}_linux_amd64/cliostore -version
```

### Docker

Fertige Multi-Arch-Images (linux/amd64 + arm64) liegen bei jedem Release in der
GitHub Container Registry:

```bash
docker run --rm -p 3000:3000 \
  -e CLIO_BOOTSTRAP_ADMIN_KEY=dein-geheimnis \
  -v clio-data:/data \
  ghcr.io/pblumer/clio:latest      # oder :v0.2.0
# Beim ersten Start wird der kid geloggt; der Schlüssel ist dann kid.secret.
```

Lokal bauen:

```bash
make docker                       # Image cliostore:<version> bauen
```

Das Image basiert auf `distroless/static` (kein Shell, nonroot-User, statisches
Binary). Die Datenbank liegt unter `/data` (Volume mounten, um Daten zu
persistieren).

### Ein Release erstellen (Maintainer)

Releases sind tag-getrieben — ein annotierter SemVer-Tag `vX.Y.Z` auf `main`
genügt, der Rest läuft automatisch:

```bash
git tag -a v0.2.0 -m "clio v0.2.0"
git push origin v0.2.0
```

Der Workflow [`release.yml`](.github/workflows/release.yml) baut daraufhin in
zwei Jobs

1. die plattform-spezifischen Archive + `checksums.txt` und hängt sie ans
   automatisch erzeugte GitHub-Release, und
2. das Multi-Arch-Image und pusht es nach `ghcr.io/pblumer/clio:vX.Y.Z` und
   `:latest`.

Die Version landet via `git describe` (bzw. dem Tag-Namen) über `-ldflags` im
Binary und ist per `cliostore -version` abrufbar. Vor dem Taggen lässt sich der
Build lokal mit `make package` proben.

### Konfiguration

| Variable          | Pflicht | Default    | Bedeutung                          |
|-------------------|---------|------------|------------------------------------|
| `CLIO_BOOTSTRAP_ADMIN_KEY` | nein* | —     | Klartext-Geheimnis, aus dem **bei leerem Schlüsselbund** ein initialer Admin-Key gebootet wird (ADR-025). Der generierte `kid` wird beim Start geloggt; der Leitungswert ist `kid.secret`. |
| `CLIO_API_TOKEN`  | nein*   | —          | **Deprecated** (ADR-008 → ADR-025): bootet bei leerem Bund einen `legacy-token`-Admin-Key. Der Leitungswert ist danach `kid.secret` — das alte `Bearer <token>` ohne `kid`-Präfix wird nicht mehr akzeptiert. |
| `CLIO_ADDR`       | nein    | `:3000`    | Listen-Adresse des HTTP-Servers    |
| `CLIO_DB_PATH`    | nein    | `clio.db`  | Pfad zur bbolt-Datenbankdatei      |
| `CLIO_DB_INITIAL_MB` | nein | `0` (aus) | Anfängliche Mmap-/Dateigröße in MiB. bbolt mappt die Datei beim Wachsen neu und hält dabei kurz einen exklusiven Lock — bei großen, gefüllten Datenbanken unter Leselast erzeugt das Schreib-Latenzspitzen. Vorab-Dimensionierung (z. B. `4096` für 4 GiB) verschiebt diese Remaps weit nach hinten und belegt die Datei real vor. Strikt grow-only: eine bereits größere DB bleibt unangetastet. `0` (Default) = bisheriges Verhalten. |
| `CLIO_DB_MONITOR_INTERVAL` | nein | `60s` | Intervall des Hintergrund-Monitors, der den genutzten Daten-Umfang gegen die vorbelegte Grenze (`CLIO_DB_INITIAL_MB`) beobachtet und warnt, bevor die bbolt-Remaps zurückkehren. Go-Dauer (z. B. `30s`); `0` schaltet ihn ab. Läuft ohnehin nur bei gesetztem `CLIO_DB_INITIAL_MB`. |
| `CLIO_DB_GROW_THRESHOLD_PCT` | nein | `80` | Schwellwert (Prozent der vorbelegten Größe), ab dem der Monitor warnt. Geklemmt auf `[1,99]`. |
| `CLIO_DB_COMPACT_ENABLED` | nein | `false` | Online-Hintergrund-Kompaktierung (defragmentiert die DB im laufenden Betrieb, ADR-015). Pro Lauf gibt es eine kurze Downtime, in der alle Zugriffe blockieren, bis die DB geschlossen, defragmentiert und neu geöffnet ist. Bei vorbelegter Datei (`CLIO_DB_INITIAL_MB`) wird die reservierte Größe danach wiederhergestellt. Truthy zum Aktivieren. |
| `CLIO_DB_COMPACT_INTERVAL_H` | nein | `6` | Intervall der Hintergrund-Kompaktierung in Stunden. Geklemmt auf `[1,168]`. |
| `CLIO_SYNC`       | nein    | `group`    | Schreibstrategie: `group`/`always`/`off` (siehe Performance) |
| `CLIO_SIGNING_KEY`| nein    | —          | base64-Ed25519-Schlüssel; aktiviert Event-Signaturen        |
| `CLIO_COMPRESS`   | nein    | `false`    | Transparente DEFLATE-Kompression der gespeicherten Event-Werte (truthy, z. B. `1`/`true`). Spart ~45–50 % Ablage je Event; wirkt nur auf neu geschriebene Events, bestehende bleiben lesbar (ADR-024). |
| `CLIO_EVENT_AUTHORSHIP` | nein | `false` | Übernimmt (truthy) den `kid` des authentifizierten Schreibers als CloudEvents-Extension `clioauthkid` in jedes neu geschriebene Event (Urheberschaft, ADR-025). Append-only-konform, in Hash/Signatur gebunden; wirkt nur auf neue Events, Default aus = byte-identisch zum bisherigen Verhalten. |
| `CLIO_DEV_MODE`   | nein    | `false`    | Dev-Mode (truthy, z. B. `1`/`true`): schaltet die destruktiven Dev-Routen (`POST /api/v1/dev/reset-database`, `POST /api/v1/dev/bulk-import-events`, `POST /api/v1/dev/close-bulk-import`) und die „Dev-Zone" im Dashboard frei. **Nicht in Produktion** (siehe ADR-022). |
| `CLIO_OBSERVE_PREAMBLE_BYTES` | nein | `4096` | Größe des Anti-Buffering-Polsters (Whitespace), das ein `observe`-Stream beim Verbindungsaufbau einmalig sendet. Manche puffernden Reverse-Proxies/Security-Gateways geben einen Stream erst weiter, wenn genug Bytes geflossen sind. Hochdrehen (z. B. `16384`/`65536`), falls Live-Events hinter einem Firmen-Proxy nicht durchkommen; `0` schaltet es ab. Vom Client als Leerzeile ignoriert. |
| `CLIO_QUERY_TIMEOUT` | nein | `0` (aus) | Maximale Laufzeit einer einzelnen `run-query`-Auswertung als Go-Dauer (z. B. `30s`, `2m`). Ein selektives Prädikat ohne `event.type ==`-Constraint scannt den gesamten Scope und hält dabei eine bbolt-Lesetransaktion — unter Schreiblast blockiert das die Wiederverwendung freier Seiten (DB-/Speicherwachstum). Die Deadline bricht solche Scans sauber ab. `0` (Default) = unbegrenzt (rückwärtskompatibel). Der `run-query`-Stream sendet unabhängig davon ein sofortiges Lebenszeichen plus periodische Heartbeats, die die Proxy-Verbindung während langer Scans offen halten. |
| `CLIO_DATA_INDEX_FIELDS` | nein | _(leer)_ | Deklariert pro Event-Typ die `event.data`-Felder, die in einen internen Sekundärindex aufgenommen werden (ADR-029), als kommagetrennte `typ:feld`-Liste — z. B. `identity.employee.new.v2:department,identity.employee.new.v2:lastName`. Ein `event.type == '<typ>' && event.data.<feld> == '<wert>'`-Prädikat wird dann per Index-Range-Scan beantwortet statt per vollständigem Typ-Scan mit Payload-Deserialisierung. Leer (Default) = kein Feld indiziert (rückwärtskompatibel). Nur **Top-Level-Felder mit String-Wert** (v1); neu deklarierte Felder werden beim nächsten Start einmalig über die vorhandenen Events nachindiziert. |
| `CLIO_PRESENCE_WINDOW` | nein | `60s` | Gleitendes „Online"-Fenster der Aktivitäts-/Presence-Sicht (ADR-030) als Go-Dauer. Ein Schlüssel ohne offene `observe`-Verbindung gilt als online, solange seine letzte erfolgreiche Aktivität jünger als dieses Fenster ist. `0` schaltet das zeitbasierte Fenster ab (dann zählt nur eine offene Live-Verbindung als online). Sichtbar über `GET /api/v1/activity` (Scope `admin`) und den `/ui`-Tab **Aktivität**. |
| `CLIO_AUTH_EVENTS` | nein | `false` | Schreibt (truthy) **Auth-Lifecycle-Events** als CloudEvents in den reservierten, server-only Namespace `/_clio/auth/…` (ADR-030, Dogfooding): `session-started`/`session-ended` (Login-Äquivalent eines sessionlosen Token-Systems) sowie `key-created`/`key-revoked`. Damit sind Login-Zeiten und Schlüssel-Lebenszyklus mit `run-query`/`observe`/UI abfragbar. Bewusst **kein** Event pro Read/Write. Default aus = kein Eingriff in den Event-Strom (rückwärtskompatibel). Client-Writes auf `/_clio/` werden stets abgelehnt (`403`). |
| `CLIO_AUTH_DENIED_EVENTS` | nein | `false` | Schreibt zusätzlich `access-denied`-Events (wiederholte 401/403 eines bekannten Schlüssels) unter `/_clio/auth/denied/…` — **rate-limitiert** je `kid` gegen Flutung. Greift nur zusammen mit `CLIO_AUTH_EVENTS`. |

\* **Auth-Material beim Start:** Ist der Schlüsselbund leer (frische DB), muss
**eines** von `CLIO_BOOTSTRAP_ADMIN_KEY` oder `CLIO_API_TOKEN` gesetzt sein —
sonst verweigert der Server den Start. Existieren bereits Schlüssel, ist beides
optional und es wird nichts gebootet.

### API-Keys (Scopes & Widerruf)

Der Zugriff läuft über benannte **API-Keys** (ADR-025). Auf der Leitung gilt das
Format `kid.secret`:

```
Authorization: Bearer kid_ci01.W8xqT2vK9pL4mN6rS1dF3hJ5
```

Jeder Key trägt **Scopes**: `read` (lesende Routen), `write` (`write-events`,
`register-event-schema`) und `admin` (Schlüsselverwaltung, Backup, Dev-Routen).
Fehlt der Schlüssel oder ist er ungültig/widerrufen/**abgelaufen** → **401**; ist
er gültig, trägt aber den nötigen Scope nicht → **403**. Gespeichert wird nur der
SHA-256-Hash des Geheimnisses, nie der Klartext; Widerruf setzt den Status (kein
Löschen). Optional pro Key: **Ablaufdatum** (`expiresAt`), **Owner/Purpose/
Description** als Inventar-Felder.

`read`/`write` lassen sich zusätzlich auf einen **Subject-Teilbaum** einschränken
(ADR-033), z. B. `read:/orders/*` oder `write:/orders/*` — der Schlüssel sieht/
beschreibt dann nur diesen Bereich; aggregat-/globale Routen (`info`, `verify`,
`event-stats`, …) verlangen einen globalen Grant. Details und Beispiele:
[`docs/security.md`](docs/security.md).

Verwaltung zur Laufzeit (alle Routen verlangen Scope `admin`):

```bash
ADMIN=kid_ab12cd34.dein-admin-geheimnis   # Bootstrap-/Admin-Key

# Neuen Key anlegen — die Antwort enthält EINMALIG den vollständigen kid.secret.
curl -sS -X POST http://127.0.0.1:3000/api/v1/keys \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name":"ci-writer","scopes":["read","write"],"owner":"team-ci","expiresAt":"2026-12-31T00:00:00Z"}'
# -> {"kid":"kid_ci01","secret":"kid_ci01.W8xq…","warning":"… nur einmal sichtbar …", …}

curl -sS -H "Authorization: Bearer $ADMIN" http://127.0.0.1:3000/api/v1/keys        # auflisten (ohne Geheimnisse)
curl -sS -X POST -H "Authorization: Bearer $ADMIN" .../api/v1/keys/kid_ci01/rotate  # Geheimnis erneuern (alter Wert ungültig)
curl -sS -X POST -H "Authorization: Bearer $ADMIN" .../api/v1/keys/kid_ci01/revoke  # widerrufen
```

**Offline / Notfall (Server gestoppt):** dieselbe Verwaltung ohne HTTP — der
Recovery-Weg bei Lockout (kein nutzbarer Admin-Key mehr):

```bash
cliostore keys create --db clio.db --name recovery-admin --scopes read,write,admin
cliostore keys list   --db clio.db
cliostore keys rotate --db clio.db --kid kid_ci01
```

Vollständiger Leitfaden inkl. sicherer Verwendung, Bootstrap-Regeln und Migration
von `CLIO_API_TOKEN`: [`docs/security.md`](docs/security.md).

Jede Autorisierungsentscheidung (allow/deny) wird strukturiert ins **slog**
geschrieben — ohne jedes Geheimnis. Zusätzlich führt clio ein **persistentes
Audit-Log** administrativer Aktionen (Key create/rotate/revoke, Schema-
Registrierung, Backup, Dev-Reset, Compaction) in einem eigenen, append-only
bbolt-Bucket (ADR-032). Es ist read-only über `GET /api/v1/audit` lesbar — mit
Scope `audit` **oder** `admin` — und nicht über die Write-API manipulierbar:

```bash
curl -sS -H "Authorization: Bearer $ADMIN" "http://127.0.0.1:3000/api/v1/audit?limit=50"
```

Details (Aktionen, Manipulationsgrenzen, Auditor-Key): [`docs/audit.md`](docs/audit.md).

Optional (`CLIO_EVENT_AUTHORSHIP`, Default aus) stempelt der Server den `kid` des
Schreibers als Extension `clioauthkid` in jedes neue Event (Urheberschaft, in die
Hash-Kette gebunden).

### Dev-Mode: Reset & Bulk-Import-Fenster

> ⚠️ Nur für Entwicklung/Demo. Ohne `CLIO_DEV_MODE` sind diese Routen **nicht
> registriert** (`404`), nicht nur gesperrt — Defense in Depth (ADR-022).

Im Dev-Mode steht ein **Bulk-Import-Fenster** für einmaliges Seeding/Migration
bereit. Es ist beim Start offen und wird durch einen Reset („Supernova") wieder
geöffnet — so ist High-Volume-Import direkt nach Start/Reset möglich, später im
Betrieb aber blockiert (Schutz vor versehentlichen Massenimporten).

| Zustand | `POST /api/v1/dev/bulk-import-events` |
|---|---|
| Fenster offen (Start / nach Reset) | **200** — Events werden geschrieben (Semantik wie `write-events`) |
| Fenster geschlossen | **409** — erst `dev/reset-database` ausführen |
| Ohne `CLIO_DEV_MODE` | **404** — Route nicht registriert |

Lebenszyklus: **Start → offen → `close-bulk-import` → 409 → `reset-database` → wieder offen**.

```bash
# 1) Supernova: Datenbank zurücksetzen → Fenster öffnet sich wieder
curl -sS -X POST "$BASE/api/v1/dev/reset-database" -H "Authorization: Bearer $TOK"

# 2) Bulk-Import (nur im offenen Fenster) — Body wie bei write-events
curl -sS -X POST "$BASE/api/v1/dev/bulk-import-events" \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"events":[{"source":"migration","subject":"/books/42","type":"book-acquired","data":{"title":"Dune"}}]}'

# 3) Fenster schließen → weitere Bulk-Importe liefern 409
curl -sS -X POST "$BASE/api/v1/dev/close-bulk-import" -H "Authorization: Bearer $TOK"
```

### Fehlerformat

Fehlerantworten folgen **RFC 7807** (`application/problem+json`):

```json
{ "type": "about:blank", "title": "Bad Request", "status": 400,
  "detail": "subject muss mit \"/\" beginnen" }
```

Alle Antworten tragen zudem `Cache-Control: no-store` (dynamische Daten, kein
Caching). Beides sind bewusste, konfliktfreie Annäherungen an die Swiss API
Guidelines (ADR-019); die übrigen Abweichungen bleiben dokumentiert (ADR-018).

### Events schreiben & lesen

```bash
# API-Key im Format kid.secret (ADR-025) — Scope muss zur Route passen
# (write-events braucht 'write'). Anlegen via POST /api/v1/keys (siehe oben).
TOKEN=kid_ci01.dein-geheimnis

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

> **Streaming & `limit`:** `read-events` streamt die Treffer als NDJSON
> (konstanter Server-Speicher, unabhängig von der Treffermenge) und kappt die
> Ausgabe ohne explizites `limit` bei einer **Default-Obergrenze** (Schutz vor
> versehentlich breiten Reads wie `/` über die gesamte Historie). Die geltende
> Obergrenze steht im Antwort-Header `X-Clio-Result-Limit`; ein höheres Limit
> setzt man explizit (`{"subject":"/","limit":1000000}` bzw. `?limit=…` auf dem
> GET-Pfad). Da gestreamt wird, ist auch ein großes Limit speicherschonend.

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

### Aktueller Zustand einer Entität (`GET /state/<subject>`)

Ein Subject ist oft eine **Entität**, die auf dem Zeitstrahl Events erfährt. Statt
die Historie selbst zu falten, liefert `GET /api/v1/state/<subject>` direkt den
**aktuellen Zustand**: die `data`-Payloads aller Events des Subjects werden in
Schreibreihenfolge per **Last-Write-Wins-Deep-Merge** zu einem Objekt verschmolzen
(ADR-039).

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/state/orders/1
# -> {"subject":"/orders/1","state":{"status":"shipped","amount":250,
#     "customer":{"id":7,"name":"Ada Lovelace"}},
#     "revision":"3","eventCount":3,"firstEventId":"1","lastEventId":"3",
#     "lastEventType":"shipped","lastEventTime":"2026-06-26T..."}

# Zeitreise: Stand „as of" einer Revision (inklusive obere Event-ID)
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/state/orders/1?at=2"

# Nur bestimmte Event-Typen in den Fold einbeziehen (wiederholbar)
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/state/orders/1?type=created&type=shipped"
```

**Merge-Semantik:** Objekte werden rekursiv pro Schlüssel verschmolzen; Skalare,
Arrays und Typwechsel ersetzen den bisherigen Wert; JSON `null` ist ein
**Tombstone** und löscht den Schlüssel. Bewusst **single-subject** (nicht
rekursiv): ein Subject = ein Aggregat. Der Zustand wird bei jedem Aufruf frisch
gefaltet (nichts wird materialisiert); ein Subject ohne passende Events ergibt
`404`. Für eigene Reduktionen (Summen, Listen) oder Cross-Subject-Aggregation baut
man weiterhin ein externes Read-Model (CQRS, siehe unten).

### Events live beobachten

`observe-events` liefert zuerst die passende History und hält die Verbindung
dann offen, um neue Events sofort als NDJSON nachzuliefern. Nach einem
Verbindungsabbruch verbindet man sich mit `lowerBound` neu und lädt so die
verpassten Events nach.

> **Hinter einem Reverse-Proxy / Firmennetz:** Der offene Stream sendet alle
> ~15 s eine Heartbeat-Leerzeile (vom Client ignoriert) und setzt
> `X-Accel-Buffering: no`. Das hält die Verbindung gegen Idle-Timeouts offen und
> verhindert, dass ein puffernder Proxy die nie endende Antwort zurückhält
> (sonst „hängt" der Live-Stream). Bei nginx zusätzlich ggf. `proxy_buffering off;`
> bzw. `proxy_read_timeout` erhöhen.

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

# Server mit Signieren starten (Leitungswert des Admin-Keys: kid.secret)
CLIO_BOOTSTRAP_ADMIN_KEY=… CLIO_SIGNING_KEY=<seed-base64> ./cliostore

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

> **Streaming & `limit`:** `run-query` streamt die Treffer als NDJSON (konstanter
> Server-Speicher) und kappt ohne explizites `limit` bei derselben
> **Default-Obergrenze** wie `read-events` (gemeldet im Header
> `X-Clio-Result-Limit`). Das schützt vor breiten Queries mit vielen Treffern;
> ein höheres Limit setzt man explizit. Ein selektives Prädikat über einen großen
> Scope kann lange scannen — die Server-`WriteTimeout` greift hier bewusst nicht.

> **Hinter einem Reverse-Proxy / Firmennetz:** Wie der `observe`-Stream sendet
> `run-query` sofort beim Verbindungsaufbau ein Lebenszeichen (Leerzeile, vom
> Client ignoriert) und danach periodische Heartbeats, und setzt
> `X-Accel-Buffering: no` plus `Cache-Control: no-store, no-transform`. Das hält
> die Verbindung offen, auch wenn ein selektives Prädikat lange scannt, bevor der
> erste Treffer kommt — sonst erreichten weder Header noch Body den Proxy und der
> setzt die Upstream-Verbindung nach seinem Read-Timeout zurück (**502 am
> Ingress**). Bei nginx ggf. `proxy_buffering off;` bzw. `proxy_read_timeout`
> erhöhen. Optional begrenzt `CLIO_QUERY_TIMEOUT` die Scan-Dauer serverseitig.

> **Index nutzbar?** Kann ein Prädikat keinen Typ-Index nutzen (kein
> `event.type ==`-Constraint), scannt es den gesamten Scope; der Server meldet das
> im Antwort-Header `X-Clio-Query-Warning` (das Dashboard zeigt dann einen
> Hinweis). Referenziert das Prädikat zusätzlich `event.data`, wird pro Event der
> Payload deserialisiert — der teuerste Fall. Abhilfe: `event.type == '…'`
> voranstellen (aktiviert den Typ-Index), engerer `subject`-Scope, kleineres
> `limit`, oder Payload-Suchen nicht zeitgleich mit hoher Schreiblast fahren.

> **Performance:** Schränkt das Prädikat den `event.type` zwingend ein
> (`event.type == 'x'`, `event.type in [...]`, auch als `&&`-Teil), nutzt
> `run-query` einen **Typ-Index** und lädt nur die passenden Events statt den
> ganzen Scope zu scannen — schnell auch über Hunderttausende Events (ADR-021).
> Lässt sich der Typ nicht sicher eingrenzen (z. B. `!=` oder ein `||` mit
> unbeschränkter Seite), wird vollständig gescannt. Bei engem `subject` plus
> Typ-Filter wählt `run-query` zudem **kostenbasiert** den selektiveren von
> Subject- und Typ-Index (ADR-023) — ein enger Stream wird also nicht durch
> einen DB-weiten Typ-Scan ausgebremst.

#### Projektion (`select`)

Optional reduziert `select` die Ausgabe auf ausgewählte Feldpfade (punkt-
separiert) — praktisch, um Bandbreite zu sparen oder nur einzelne Felder zu
lesen. Die Verschachtelung bleibt erhalten (`data.title` →
`{"data":{"title":...}}`), fehlende Felder werden ausgelassen (kein `null`).

```bash
curl -X POST http://127.0.0.1:3000/api/v1/run-query \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/orders","recursive":true,
       "select":["id","subject","data.amount"]}'
# -> {"id":"1","subject":"/orders/1","data":{"amount":250}}
#    {"id":"2","subject":"/orders/2"}
```

Top-Level-Pfade sind CloudEvents-Feldnamen (`id`, `subject`, `type`, `data` …);
innerhalb von `data` ist beliebige Verschachtelung möglich. Array-Indizierung
wird nicht unterstützt. Ohne `select` wird das volle Event zurückgegeben.

> **Aggregation, Joins, Reporting?** Bewusst **nicht** im Kern. clio ist der
> append-only Source of Truth; abgeleitete **Read Models** baut man außerhalb
> (CQRS). Das Referenzbeispiel
> [`examples/projection-worker-postgres`](./examples/projection-worker-postgres/)
> zeigt einen Projection Worker mit `observe`, Checkpointing, Idempotenz, Replay
> und Lag-Monitoring gegen ein PostgreSQL-Read-Model.

### Verfügbare Event-Typen

```bash
# Alle bisher geschriebenen Typen (mit Anzahl), als NDJSON
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/read-event-types
# -> {"type":"acquired","count":2}
#    {"type":"borrowed","count":1}
```

### Vorhandene Subjects (Streams)

```bash
# Alle bisher beschriebenen Subjects (mit Anzahl), als NDJSON, alphabetisch
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/read-subjects
# -> {"subject":"/books/42","count":2}
#    {"subject":"/books/99","count":1}
#    {"subject":"/movies/7","count":1}

# Nur Subjects unterhalb eines Pfads (rekursiver Scope)
curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:3000/api/v1/read-subjects?prefix=/books"
# -> {"subject":"/books/42","count":2}
#    {"subject":"/books/99","count":1}
```

`prefix` schränkt auf den Stream-Pfad und alles darunter ein; Prefix-Geschwister
(z. B. `/booksstore` zu `/books`) werden korrekt ausgeschlossen. Praktisch, um
zu sehen, welche Streams existieren, oder um eine UI hierarchisch zu befüllen.

Mit `tree=true` kommt stattdessen ein **hierarchischer Baum** als ein JSON-Objekt;
`count` sind die Events direkt auf einem Subject, `total` die Summe im Teilbaum:

```bash
curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:3000/api/v1/read-subjects?tree=true"
```
```json
{ "subject": "/", "count": 0, "total": 3, "children": [
    { "subject": "/books", "count": 0, "total": 2, "children": [
        { "subject": "/books/42", "count": 2, "total": 2, "children": [] }
    ]},
    { "subject": "/movies", "count": 0, "total": 1, "children": [
        { "subject": "/movies/7", "count": 1, "total": 1, "children": [] }
    ]}
]}
```

`prefix` setzt im Tree-Modus die Wurzel (`?tree=true&prefix=/books`).

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

> **Sicherheitshinweis:** `/metrics` ist **bewusst ohne Auth** erreichbar — das
> ist die übliche Konvention für Prometheus-Scraping im internen Netz (ADR-013).
> Die Metriken enthalten keine Event-Inhalte, aber Betriebsdaten (Routen,
> Request-Raten, DB-Größe). **Nicht öffentlich exponieren**: per Reverse-Proxy,
> Firewall oder Netzwerksegmentierung auf das interne Scraping-Ziel beschränken.

Enthalten u. a.: `clio_http_requests_total{method,route,status}`,
`clio_http_request_duration_seconds` (Histogramm), `clio_events_written_total`,
`clio_precondition_failures_total`, `clio_active_observers`, `clio_events_total`,
`clio_db_size_bytes` (Dateigröße) samt `clio_db_used_bytes`/`clio_db_free_bytes`
(belegt/wiederverwendbar). Laufzeit-Metriken: `clio_memory_heap_bytes`,
`clio_memory_sys_bytes`, `clio_goroutines`, `clio_num_cpu` und —
plattformabhängig (Linux/macOS via getrusage) — `clio_process_cpu_seconds_total`.

### Wartung: Kompaktierung & DB-Füllgrad

Die angezeigte **DB-Größe** ist die **Dateigröße auf der Platte** (`os.Stat`),
nicht das Datenvolumen. bbolt vergrößert die Datei bei Bedarf, gibt sie aber
**nie von selbst frei** — freie Seiten (etwa nach einem Dev-Reset) werden zuerst
wiederverwendet, die Datei wächst also erst wieder, wenn die Nutzdaten die
bisherige Größe übersteigen. Deshalb „bleibt" die Größe nach einem Reset stehen,
obwohl kaum Events vorhanden sind.

Wie viel der Datei tatsächlich belegt ist, zeigt der **Füllgrad**: `/api/v1/info`
liefert `databaseFileBytes` / `databaseUsedBytes` / `databaseFreeBytes` /
`databaseFillPercent`, als Prometheus-Gauges `clio_db_used_bytes` /
`clio_db_free_bytes`. Im **Dashboard** stellt die Karte „DB-Größe" das als
Füllbalken dar (belegt vs. wiederverwendbar). Die echte Wachstumsgrenze ist der
**Plattenplatz**; freien Anteil gewinnst du mit `compact` zurück.

`compact` defragmentiert die bbolt-Datei **offline** (atomarer Swap), ohne
Events zu löschen oder zu verändern — die Hash-Kette bleibt gültig:

```bash
# Server vorher stoppen (der Befehl scheitert sonst am Datei-Lock)
CLIO_DB_PATH=clio.db ./cliostore compact
# -> kompaktiert: clio.db — 2097152 -> 1048576 bytes (50.0% kleiner)
```

### Backup, Restore & Verify

Clio sichert sich über **konsistente Snapshots** der bbolt-Datei (ADR-031). Ein
Snapshot ist selbst eine gültige, per Hash-Kette prüfbare `.clio`-Datei:

```bash
# Hot-Backup im laufenden Betrieb (admin-scoped HTTP-Endpunkt, blockiert keine Schreiber)
curl -fsS -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3000/api/v1/backup -o clio-$(date +%F).clio

# Cold-Backup über CLI (Server gestoppt) — schreibt + verifiziert in einem Schritt
./cliostore backup --db clio.db --output clio-$(date +%F).clio --verify

# Restore (offline) an einen Zielpfad, dann prüfen
./cliostore restore --input clio-2026-06-18.clio --db /data/clio.db
./cliostore verify  --db /data/clio.db   # Exit 0 = Kette intakt, 1 = Bruch
```

`verify` ist read-only und skriptbar (Exit-Code); ist `CLIO_SIGNING_KEY` gesetzt,
prüft es auch die Event-Signaturen mit. **Wichtig:** Das CLI-`backup` kann eine
*laufende* Instanz nicht öffnen (bbolt-Datei-Lock) — dafür ist der HTTP-Endpunkt
da. Vollständige Anleitung samt Garantien, Fehlerfällen und RPO/RTO:
[`docs/backup-restore.md`](docs/backup-restore.md).

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

**Lesen:** Nicht-rekursive Reads laufen über den Subject-Index (nur die Events
des Subjects). Rekursive Reads eines **Teilbaums** sind ebenfalls
index-begrenzt — die Laufzeit hängt von der Treffermenge ab, nicht von der
Gesamtzahl der Events (ein 10-Event-Teilbaum aus 50.000 Events: ~0,6 ms statt
~250 ms). Nur die echte Wurzel-Abfrage (`/`, „alle Events") ist naturgemäß
O(alle).

```bash
# Benchmarks selbst ausführen
go test -run='^$' -bench=BenchmarkRead -benchmem ./internal/store/   # Lesen
go test -run='^$' -bench=BenchmarkAppend -benchmem ./internal/store/ # Schreiben
```

## Tests

```bash
go test ./...
go test -race ./...   # Nebenläufigkeit (Observe, Group Commit)
make cover            # paketübergreifende Gesamt-Coverage (~87 %)
```

Welche Bereiche abgedeckt sind und welche Lücken bewusst offen bleiben:
[`docs/testing.md`](docs/testing.md).

## Betrieb (Deployment-Vorlagen & Profile)

Fertige Beispielkonfigurationen für systemd, Docker Compose, Windows und
Kubernetes (Single-Instance, mit Skalierungs-Warnung) liegen unter
[`deploy/`](deploy/); empfohlene Umgebungsvariablen je Profil (`dev`/`lab`/
`small-production`) in [`docs/operations-profiles.md`](docs/operations-profiles.md).
Eignung und Garantien: [`docs/production-readiness.md`](docs/production-readiness.md).
