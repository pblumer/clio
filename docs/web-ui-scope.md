# Web-UI — Machbarkeits- & Scope-Skizze

> Status: **Stufen 1 (Dashboard) + 2 (Live-Event-Viewer) + 3 (Subject-Browser) + 5 (Query-Konsole &amp; Hilfe) + 6 (Sci-Fi-Theme &amp; Telemetrie) + 7 (Event-/Schema-Generator) + 8 (Eventstrom-Dashboard) umgesetzt** · Stufe 4 skizziert.
> Zugehörige Entscheidung: **ADR-020** in [`ARCHITECTURE.md`](../ARCHITECTURE.md).

Ein schlankes Web-UI für **Maintenance, Observing, Monitoring** — ohne Clios
Kernprinzip („ein abhängigkeitsfreies Single-Binary, alles eingebettet, kein
Internet nötig") zu verwässern.

## 1. Motivation

Betriebssichtbarkeit gibt es heute nur **maschinenlesbar**: `/metrics`
(Prometheus-Text, ADR-013) und `/api/v1/info`. Für „mal eben draufschauen"
fehlt die menschenlesbare Klammer. Die „Betrieb"-Rolle des Learning Paths
hatte bislang kein UI. Prometheus/Grafana sind mächtig, aber für einen
einzelnen Single-Binary-Store oft überdimensioniert und ein externer
Zusatzdienst — gegen den Anspruch „läuft allein, ohne Setup".

## 2. Leitplanken (nicht verhandelbar)

Damit das UI kein Fremdkörper wird, gelten dieselben Prinzipien wie für die
eingebettete Swagger UI (ADR-011):

- **`go:embed`** für alle Assets — kein npm, kein Bundler, kein CDN, kein
  Build-Step.
- **Vanilla JS + Inline-CSS**, kein React/Vue/Svelte. Eine einzige HTML-Datei.
- **Reine View-Schicht** auf bestehende Endpunkte — **keine neue
  Privilegien-Ebene**, keine neuen Schreibpfade.
- **Auth wie der Rest:** Bearer-Token, same-origin, kein CORS. Die Seite selbst
  ist statisch und damit unauth (wie `/docs`); die *Daten* sind es nicht.
- **`/metrics` bleibt die Quelle der Wahrheit** für Alerting/Zeitreihen — das
  UI ersetzt Prometheus nicht, es ergänzt es für den Sofort-Blick.

## 3. Machbarkeit

Clio bringt alle Bausteine bereits mit — das UI ist überwiegend eine
Präsentationsschicht:

| Bedarf des UI            | Vorhandener Endpunkt                                   |
| ------------------------ | ----------------------------------------------------- |
| Version, Uptime, Sync    | `GET /api/v1/info`                                     |
| Events total, DB-Größe   | `/metrics` (`clio_events_total`, `clio_db_size_bytes`)|
| Aktive Observer          | `/metrics` (`clio_active_observers`)                  |
| Req-Rate, Latenz p50/p99 | `/metrics` (`clio_http_request_*`, Histogramm)        |
| Live-Event-Stream        | `observe-events` / `GET …/events/<subject>?watch=true`|
| Subject-/Typ-Navigation  | `…/events/<subject>`, `read-event-types`              |
| Schema-Ansicht           | `read-event-schema`                                   |
| Integrität               | `GET /api/v1/verify`, `GET /api/v1/public-key`        |

Risiko/Aufwand sind gering: keine serverseitige Template-Engine, keine
Session-Verwaltung, keine zusätzlichen Go-Abhängigkeiten. Das Prometheus-
Textformat lässt sich im Browser in ~30 Zeilen parsen; Perzentile werden — wie
`histogram_quantile` in PromQL — aus dem kumulativen Histogramm interpoliert.

## 4. Scope in Stufen

### Stufe 1 — Dashboard / Health-Übersicht  ✅ umgesetzt
Eine Seite unter `/ui`, die `/api/v1/info` + `/metrics` rendert: Version,
Uptime, Events total, DB-Größe, aktive Observer, geschriebene Events,
Precondition-Fehler, HTTP-Anfragen + clientseitig berechnete Rate, Latenz
p50/p99. Token-Eingabe (im Tab via `sessionStorage`), wählbares
Auto-Refresh-Intervall, Status-/Fehleranzeige.

### Stufe 2 — Live Event-Viewer  ✅ umgesetzt
Zweiter Tab im `/ui`. Subject-Eingabe → `GET …/events/<subject>?watch=true`
per `fetch()`-Stream (ReadableStream-Reader, inkrementelles NDJSON-Parsing;
EventSource scheidet wegen Bearer-Header aus) → erst History, dann live, mit
Typ-Filter, rekursiv-Schalter, Pause/Fortsetzen (gepuffert, „N neue"-Badge) und
aufklappbarer `data` je Event. Kein neuer Server-Code — nutzt den bestehenden
Streaming-Endpunkt.

### Stufe 3 — Subject-Browser  ✅ umgesetzt
Dritter Tab „Explorer" im `/ui`. Read-only: navigierbarer, ein-/ausklappbarer
Subject-Baum aus `read-subjects?tree=true` (Klick auf ein Subject lädt dessen
Events via `…/events/<subject>?recursive=false`), Event-Typen mit Anzahl und
aufklappbaren JSON-Schemas (`read-event-types` / `read-event-schema`) sowie ein
Integritäts-Panel (`verify` / `public-key`). Erneut kein neuer Server-Code —
alle Endpunkte existierten bereits.

### Stufe 5 — Query-Konsole &amp; Hilfe  ✅ umgesetzt
Vierter Tab „Query" im `/ui`: eine `run-query`-Konsole mit einem leichtgewichtigen
**CEL-Editor** (Vanilla JS, kein CDN/Bundler — Token-Overlay fürs
Syntax-Highlighting hinter einem transparenten `textarea`). IDE-artige
Unterstützung: kontextsensitive Autovervollständigung für `event`-Felder,
stdlib-Funktionen/Makros und — aus einer Stichprobe echter Events gelernte —
`event.data.*`-Pfade; <kbd>Ctrl/Cmd</kbd>+<kbd>Enter</kbd> führt aus,
<kbd>Tab</kbd> übernimmt einen Vorschlag. Fehler aus `run-query` (HTTP 400
`problem+json`, inkl. CEL-Compilermeldung) werden inline wie ein Linter gezeigt.
Scope-/Projektions-Optionen (`recursive`, `limit`, `lowerBound`/`upperBound`,
`select`) sind direkt bedienbar. Ein fünfter Tab „Hilfe" dokumentiert die
`event`-Felder, Operatoren/Funktionen und Projektion und bietet Beispiele, die
sich in den Editor laden lassen. Dazu **Verlauf &amp; Favoriten** (persistent
im Browser via `localStorage`) und **Export** der Ergebnisse als NDJSON oder CSV
(verschachtelte Felder als punktierte Spalten). Kein neuer Server-Code — nur das
bestehende `run-query`.

### Stufe 6 — Sci-Fi-Theme, Telemetrie &amp; Liveness-EKG  ✅ umgesetzt
Optischer Umbau des `/ui` in einen **HUD-/Space-Stil** (Sternenfeld via
CSS-Gradients, Neon-Glow, Monospace-HUD) — weiterhin Vanilla, kein CDN/Build.
Neu auf dem Dashboard:

- **Liveness-EKG** (~~Oszilloskop-Sweep auf jeden `ping`~~): in **Stufe 8 entfernt**
  und durch das Eventstrom-Diagramm ersetzt (siehe unten).
- **Live-Telemetrie-Charts**: glühende Sparklines (Canvas) für CPU-Last,
  Heap-Speicher, Event-Durchsatz und Request-Rate, je aus rollierenden
  Messfenstern.

Dafür **einzige Server-Erweiterung** der gesamten UI-Reihe: das `metrics`-Paket
exponiert zusätzliche Laufzeit-Serien aus der Standardbibliothek
(`runtime/metrics`: Heap/Sys-Speicher, Goroutinen; `runtime.NumCPU`) sowie —
plattformabhängig via `getrusage` (Linux/macOS, inkl. Docker) —
`clio_process_cpu_seconds_total`. Keine neuen Fremd-Abhängigkeiten (ADR-001).

### Stufe 7 — Event-/Schema-Generator  ✅ umgesetzt
Sechster Tab „Erzeugen" im `/ui` — als **Onboarding-Hilfe**: ein einfaches
Formular schreibt Events (`POST /api/v1/write-events`) mit Subject/Typ/Source und
optionalem JSON-`data`, inkl. Vorlagen (Bibliothek/Autoverleih/Bestellung),
Mehrfach-Erzeugung und **Ein-Klick-Beispiel-Szenarien** unter einem Prefix
(damit Neueinsteiger sofort etwas zum Beobachten/Erkunden haben). Optional lassen
sich **JSON-Schemas registrieren** (`POST /api/v1/register-event-schema`), mit
klarer Rückmeldung bei 409 (unveränderlich) und 400 (Validierung). Kein neuer
Server-Code — beide Endpunkte existierten bereits.

Dies ist die **erste bewusst schreibende** UI-Fläche. Die Abgrenzung zu Stufe 4:
Es sind **normale Daten-Writes**, die jede:r mit dem Token ohnehin per API
ausführen kann (keine neue Privilegien-Ebene, kein neuer Endpunkt) — im
Unterschied zu **destruktiven Maintenance-Operationen** (Kompaktierung etc.),
die weiterhin zurückgestellt bleiben.

### Stufe 8 — Eventstrom-Dashboard  ✅ umgesetzt
Umbau des Dashboards weg vom dekorativen **Liveness-EKG** (Stufe 6) hin zur
Beobachtung des **Eventstroms**. Ein einziger `observe`-Stream auf `/` (rekursiv,
`GET /api/v1/events?watch=true&recursive=true`) speist zwei Ansichten:

- **Live-Eventstrom-Liniendiagramm**: Der Stream liefert **nur neue Events** ab
  dem Verbinden — `lowerBound` wird hinter die höchste Event-ID gesetzt
  (`= eventsTotal + 1` aus `/api/v1/info`; IDs sind global monoton), sodass keine
  History übertragen wird. Aus dem `time` jedes Events wird die Verteilung über
  die Achse `[Beobachtungsbeginn … jetzt]` als **glühende Linie** (Canvas)
  gezeichnet — umschaltbar **Rate** (Events je Zeitabschnitt) bzw. **kumuliert**.
  Der „jetzt"-Rand wandert über einen 1-Sekunden-Takt, neue Events aktualisieren
  sofort.
- **Einklappbares Live-Events-Fenster**: derselbe Stream füllt eine Liste
  (neueste oben, aufklappbare `data`, gekappt). Auf-/Zuklapp-Zustand wird im
  Browser gemerkt (`localStorage`).

Kein neuer Server-Code — nutzt den bestehenden Streaming-Endpunkt (`observe` mit
`lowerBound`). Die Telemetrie-Sparklines aus Stufe 6 bleiben erhalten.

> Hinweis: Eine frühere Variante streamte die **gesamte** History und zeigte
> „seit Serverstart"; das Diagramm bildet nun bewusst nur den **laufenden**
> Strom ab dem Verbinden ab (Säulen → Linie).

### Stufe 4 — Maintenance-Konsole  ⚠️ bewusst zurückgestellt
Schreibende **Maintenance**-Aktionen (z. B. Kompaktierung anstoßen). **Out of
scope** für jetzt: würde über die normalen Daten-Writes (Stufe 7) hinaus eine
betriebskritische Angriffsfläche schaffen und eine eigene Absicherung erfordern.
Erst, wenn ein klares Auth-/Audit-Konzept dafür steht.

## 5. Bewusst *nicht* im Scope

- **Historische Zeitreihen / Alerting** — dafür bleibt `/metrics` + Prometheus
  der Weg. Das UI zeigt Live-Momentaufnahmen.
- **Frontend-Toolchain** (npm/Bundler/SPA) — Widerspruch zu ADR-001.
- **Mehrbenutzer-/Rollenmodell, Sessions, Cookies** — es bleibt beim einen
  Bearer-Token (ADR-008).
- **Schreibende _Maintenance_-Operationen** über das UI (Stufe 4) — die normalen
  Daten-Writes (Events/Schemas, Stufe 7) sind hingegen bewusst enthalten.

## 6. Sicherheitsbetrachtung

- Die ausgelieferte Seite ist statisch und gibt **keine** internen Daten preis
  (wie `/docs`) → unauth vertretbar.
- Alle **Daten**-Abrufe nutzen das Bearer-Token; `/api/v1/info` verlangt es,
  `/metrics` ist (wie gehabt, ADR-013) unauth und im Betrieb per Netz/Proxy
  abzusichern.
- Same-origin, kein CORS. Das Token liegt nur clientseitig im Tab
  (`sessionStorage`), nicht persistent und nicht serverseitig.
- Der „Erzeugen"-Tab (Stufe 7) schreibt **über bestehende Endpunkte**
  (`write-events`, `register-event-schema`) mit demselben Bearer-Token → **keine
  neuen Endpunkte, keine neue Privilegien-Ebene**. Ohne Token kein Write (wie bei
  den Lese-Aufrufen). Die Angriffsfläche gegenüber dem bestehenden API wächst
  damit nicht; betriebskritische **Maintenance**-Writes bleiben separat (Stufe 4).

## 7. Aufwandseinschätzung

| Stufe | Aufwand            | Neue Abhängigkeiten |
| ----- | ------------------ | ------------------- |
| 1     | ~1 Tag (erledigt)  | keine               |
| 2     | ~1–2 Tage          | keine               |
| 3     | ~1–2 Tage          | keine               |
| 5     | ~1–2 Tage (erledigt) | keine             |
| 6     | ~1–2 Tage (erledigt) | keine (nur stdlib runtime/metrics + getrusage) |
| 7     | ~1 Tag (erledigt)  | keine               |
| 4     | offen (Auth-Konzept zuerst) | ggf. Audit-Log |
