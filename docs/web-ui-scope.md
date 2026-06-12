# Web-UI — Machbarkeits- & Scope-Skizze

> Status: **Stufe 1 (Dashboard) umgesetzt** · Stufen 2–4 skizziert.
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

### Stufe 2 — Live Event-Viewer  ⏳ vorgesehen
Subject-Eingabe → `observe-events` per Stream → Events tröpfeln live rein, mit
Typ-Filter, Pause/Resume und „History laden". Das „Wow", weil Clio ein
*Event*-Store ist und man das heute nur per `curl` sieht.

### Stufe 3 — Subject-Browser  ⏳ vorgesehen
Read-only Explorer über den Subject-Baum (`recursive`), Event-Typen pro Subject
und registrierte Schemas. Plus Integritäts-Panel (`verify`, `public-key`).

### Stufe 4 — Maintenance-Konsole  ⚠️ bewusst zurückgestellt
Schreibende Aktionen (z. B. Kompaktierung anstoßen). **Out of scope** für jetzt:
würde aus dem „kleinen UI" eine Angriffsfläche machen und eine eigene
Absicherung erfordern. Erst, wenn ein klares Auth-/Audit-Konzept dafür steht.

## 5. Bewusst *nicht* im Scope

- **Historische Zeitreihen / Alerting** — dafür bleibt `/metrics` + Prometheus
  der Weg. Das UI zeigt Live-Momentaufnahmen.
- **Frontend-Toolchain** (npm/Bundler/SPA) — Widerspruch zu ADR-001.
- **Mehrbenutzer-/Rollenmodell, Sessions, Cookies** — es bleibt beim einen
  Bearer-Token (ADR-008).
- **Schreibende Operationen** über das UI (außer später Stufe 4 unter
  eigener Absicherung).

## 6. Sicherheitsbetrachtung

- Die ausgelieferte Seite ist statisch und gibt **keine** internen Daten preis
  (wie `/docs`) → unauth vertretbar.
- Alle **Daten**-Abrufe nutzen das Bearer-Token; `/api/v1/info` verlangt es,
  `/metrics` ist (wie gehabt, ADR-013) unauth und im Betrieb per Netz/Proxy
  abzusichern.
- Same-origin, kein CORS. Das Token liegt nur clientseitig im Tab
  (`sessionStorage`), nicht persistent und nicht serverseitig.
- Keine neuen Endpunkte mit Schreibwirkung → keine Vergrößerung der
  Angriffsfläche gegenüber dem bestehenden API.

## 7. Aufwandseinschätzung

| Stufe | Aufwand            | Neue Abhängigkeiten |
| ----- | ------------------ | ------------------- |
| 1     | ~1 Tag (erledigt)  | keine               |
| 2     | ~1–2 Tage          | keine               |
| 3     | ~1–2 Tage          | keine               |
| 4     | offen (Auth-Konzept zuerst) | ggf. Audit-Log |
