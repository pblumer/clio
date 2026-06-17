// Package webui bettet ein schlankes Betriebs-Dashboard ins Binary ein und
// liefert es unter GET /ui aus. Die Seite selbst ist statisch (eine einzige,
// abhängigkeitsfreie HTML-Datei mit Inline-CSS/-JS, kein Build-Step, kein CDN)
// und damit — wie die Swagger UI (ADR-011) — ohne Auth erreichbar. Die
// angezeigten Daten holt sie clientseitig von /api/v1/info (Bearer-Token) und
// /metrics; das Token gibt der Nutzer in der Seite ein (same-origin).
package webui

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
)

// dashboardHTML ist die eingebettete Dashboard-Seite.
//
//go:embed dashboard.html
var dashboardHTML []byte

// dashboardETag ist ein starker, inhaltsbasierter ETag. Da die Seite ins Binary
// eingebettet ist, ändert sich ihr Inhalt nur mit einem neuen Build/Release —
// dann ändert sich auch der ETag, und Clients laden sofort die neue Seite.
// Solange unverändert, kann der Server mit 304 antworten.
var dashboardETag = func() string {
	sum := sha256.Sum256(dashboardHTML)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}()

// Handler liefert das Dashboard als statisches HTML aus.
//
// Caching: bewusst `no-cache` (nicht `max-age`) plus inhaltsbasierter ETag. Das
// heißt „cachen erlaubt, aber vor jeder Nutzung revalidieren": Der Client schickt
// `If-None-Match`; bei unverändertem Inhalt kommt ein günstiges 304, nach einem
// Deploy mit geänderter Seite sofort die neue Version. Ein `max-age`-Cache (wie
// zuvor) hätte nach einem Deploy bis zum Ablauf eine veraltete Dashboard-Version
// ausgeliefert — inklusive fehlender neuer Funktionen.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", dashboardETag)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Header.Get("If-None-Match") == dashboardETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(dashboardHTML)
	})
}
