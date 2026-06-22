# Modularisierungsplan: `internal/webui/dashboard.html`

> **Status:** Vorschlag / noch nicht umgesetzt.
> **Ziel:** Die sehr große Single-File-UI (~2960 Zeilen) risikoarm und
> schrittweise modularisieren — **ohne** neue externe Abhängigkeiten, **ohne**
> Build-Step/CDN und **ohne** Verhaltens-/DOM-Änderung. Bewahrt die
> Architekturprinzipien aus ADR-020 (eingebettet via `go:embed`, Vanilla JS,
> reine View-Schicht, gleiche Bearer-Token-Auth).

## Ausgangslage

`internal/webui/dashboard.html` enthält Markup, CSS und das gesamte JavaScript
für sieben Tabs (Dashboard, Live-Events, Explorer, Erzeugen, Keys, Query, Hilfe)
inline in **einer** Datei. Das erschwert Review, Navigation und gezielte
Änderungen und vermischt unzusammenhängende Belange.

Randbedingungen (ADR-020):

- **Kein Build-Step, kein CDN, keine npm-Toolchain** — die Auslieferung bleibt
  statisch und eingebettet.
- Assets werden weiterhin per `go:embed` aus `internal/webui` ausgeliefert.
- Same-origin, keine CORS-Änderung; Token bleibt in `sessionStorage`.
- Reine View-Schicht auf bestehende Endpunkte — **keine** neuen Routen/Privilegien.

## Zielstruktur (Vorschlag)

```
internal/webui/
  webui.go              # go:embed + Handler() (ggf. embed.FS statt Einzeldatei)
  static/
    dashboard.html      # nur noch Markup + <link>/<script src> Verweise
    css/
      dashboard.css     # ausgelagertes CSS
    js/
      core.js           # gemeinsame Helfer: fetch-Wrapper, Auth/Token, Tab-Routing
      dashboard.js      # Tab: Dashboard (Telemetrie, Eventstrom-Chart, Box-Zoom)
      live-events.js    # Tab: Live-Events (observe-Stream)
      explorer.js       # Tab: Explorer (Subject-Baum, Typen/Schemas, verify)
      generate.js       # Tab: Erzeugen (write-events, register-event-schema)
      keys.js           # Tab: Keys (admin: /api/v1/keys*)
      query.js          # Tab: Query (CEL-Editor, Verlauf/Favoriten, Export)
      help.js           # Tab: Hilfe (CEL-Referenz)
```

> Hinweis: Wenn `Handler()` heute eine einzelne Datei ausliefert, im selben
> Schritt auf `embed.FS` + `http.FileServer` (bzw. eine kleine Mux-Erweiterung)
> umstellen, damit `/ui/css/*` und `/ui/js/*` ohne Auth wie die Hauptseite
> bereitstehen (analog `/docs`-Assets). Pfade relativ halten, damit kein
> Routing-Verhalten kippt.

## Schrittweise Umsetzung (je ein kleiner, reviewbarer PR)

Jeder Schritt hält das **gerenderte DOM und das Laufzeitverhalten byte-/
verhaltensgleich** — es wird nur verschoben, nicht umgeschrieben.

1. **Embedding vorbereiten.** `webui.go` auf `embed.FS` umstellen und das
   Asset-Serving (`/ui`, `/ui/css/*`, `/ui/js/*`) einrichten, ohne Inhalte zu
   verschieben (dashboard.html bleibt vorerst unverändert). Smoke-Test grün.
2. **CSS auslagern.** Inline-`<style>` → `static/css/dashboard.css`, per
   `<link rel="stylesheet">` einbinden. Reiner Move.
3. **Gemeinsame Helfer auslagern.** Token-Handling, `fetch`-Wrapper,
   Tab-Routing und geteilte Utilities → `static/js/core.js` (`<script src>`,
   bewusst klassisch ohne ES-Module, um Verhalten/Reihenfolge zu erhalten).
4. **Tabs einzeln auslagern.** Pro PR genau **ein** Tab-JS herausziehen
   (`dashboard.js`, dann `live-events.js`, `explorer.js`, `generate.js`,
   `keys.js`, `query.js`, `help.js`). Kleiner Diff, klar abgrenzbar.
5. **Aufräumen.** `dashboard.html` enthält am Ende nur noch Markup +
   Asset-Verweise; tote Inline-Reste entfernen.

## Verifikation pro Schritt

- `make smoke` (Server hochfahren + Postman/Newman) bleibt grün.
- Manueller `/ui`-Check: jeder Tab lädt, verbindet mit Token, streamt/queryt wie
  zuvor; Box-Zoom, Reconnect und Export funktionieren unverändert.
- `go test ./...` und `go vet ./...` grün; `gofmt`-konform.
- Screenshot-Vergleich des Dashboards (`docs/screenshots/dashboard.png`) als
  optionale visuelle Regression.

## Bewusste Nicht-Ziele

- **Kein** Framework, kein Bundler, kein TypeScript, kein CDN.
- **Keine** funktionalen Erweiterungen in diesem Vorhaben (reines Refactoring).
- **Keine** Änderung an API-Endpunkten, Auth-Semantik oder Datenformaten.
- ES-Module (`type="module"`) sind optional und erst nach erfolgtem Split zu
  erwägen — sie ändern Lade-/Scope-Semantik und gehören in einen separaten,
  bewusst getesteten Schritt.
