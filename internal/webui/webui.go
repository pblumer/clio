// Package webui bettet ein schlankes Betriebs-Dashboard ins Binary ein und
// liefert es unter GET /ui aus. Die Seite selbst ist statisch (eine einzige,
// abhängigkeitsfreie HTML-Datei mit Inline-CSS/-JS, kein Build-Step, kein CDN)
// und damit — wie die Swagger UI (ADR-011) — ohne Auth erreichbar. Die
// angezeigten Daten holt sie clientseitig von /api/v1/info (Bearer-Token) und
// /metrics; das Token gibt der Nutzer in der Seite ein (same-origin).
package webui

import (
	_ "embed"
	"net/http"
)

// dashboardHTML ist die eingebettete Dashboard-Seite.
//
//go:embed dashboard.html
var dashboardHTML []byte

// Handler liefert das Dashboard als statisches HTML aus. Die Observability-
// Middleware setzt standardmäßig Cache-Control: no-store; da die Seite statisch
// ist, erlauben wir hier bewusst kurzes Caching.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(dashboardHTML)
	})
}
