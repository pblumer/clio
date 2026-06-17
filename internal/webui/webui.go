// Package webui bettet ein schlankes Betriebs-Dashboard ins Binary ein und
// liefert es unter GET /ui aus. Die Seite ist statisch (Vanilla, kein
// Build-Step, kein CDN) und damit — wie die Swagger UI (ADR-011) — ohne Auth
// erreichbar. Die angezeigten Daten holt sie clientseitig von /api/v1/info
// (Bearer-Token) und /metrics; das Token gibt der Nutzer in der Seite ein
// (same-origin).
//
// Aufbau: Das Markup liegt in static/dashboard.html, die ausgelagerten Assets
// (z. B. static/css/dashboard.css) im static-Verzeichnis. Alles wird per
// go:embed eingebettet; Handler() liefert die Seite unter /ui, AssetHandler()
// die übrigen Dateien unter /ui/<pfad>.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"mime"
	"net/http"
	"path"
)

// assets bündelt die eingebetteten Dashboard-Dateien (HTML + Assets).
//
//go:embed static
var assets embed.FS

// dashboardHTML ist die eingebettete Dashboard-Seite.
var dashboardHTML = mustReadAsset("static/dashboard.html")

// dashboardETag ist ein starker, inhaltsbasierter ETag. Da die Seite ins Binary
// eingebettet ist, ändert sich ihr Inhalt nur mit einem neuen Build/Release —
// dann ändert sich auch der ETag, und Clients laden sofort die neue Seite.
// Solange unverändert, kann der Server mit 304 antworten.
var dashboardETag = etagOf(dashboardHTML)

func mustReadAsset(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		// Eingebettete Datei — fehlt sie, ist der Build kaputt (Programmierfehler).
		panic("webui: eingebettetes asset fehlt: " + name + ": " + err.Error())
	}
	return b
}

// etagOf bildet einen starken, inhaltsbasierten ETag über die Bytes.
func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// Handler liefert das Dashboard (static/dashboard.html) als statisches HTML aus.
//
// Caching: bewusst `no-cache` (nicht `max-age`) plus inhaltsbasierter ETag. Das
// heißt „cachen erlaubt, aber vor jeder Nutzung revalidieren": Der Client schickt
// `If-None-Match`; bei unverändertem Inhalt kommt ein günstiges 304, nach einem
// Deploy mit geänderter Seite sofort die neue Version. Ein `max-age`-Cache (wie
// zuvor) hätte nach einem Deploy bis zum Ablauf eine veraltete Dashboard-Version
// ausgeliefert — inklusive fehlender neuer Funktionen.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAsset(w, r, dashboardHTML, "text/html; charset=utf-8", dashboardETag)
	})
}

// AssetHandler liefert die übrigen eingebetteten Dashboard-Assets unter
// /ui/<pfad> aus (z. B. /ui/css/dashboard.css). Erwartet das gematchte
// Pfad-Segment in r.PathValue("path"). Das nackte /ui/ (leerer Pfad) leitet wie
// bisher auf die kanonische /ui um. Unbekannte Pfade ergeben 404.
func AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := r.PathValue("path")
		if rel == "" {
			http.Redirect(w, r, "/ui", http.StatusMovedPermanently)
			return
		}
		// Pfad säubern und am static-Verzeichnis verankern: ein führender Slash mit
		// anschließendem path.Clean lässt "../" nicht aus dem Verzeichnis ausbrechen.
		name := "static" + path.Clean("/"+rel)
		body, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		writeAsset(w, r, body, ct, etagOf(body))
	})
}

// writeAsset schreibt ein eingebettetes Asset mit inhaltsbasiertem ETag und
// `no-cache` (revalidieren). Passendes If-None-Match ergibt ein günstiges 304.
func writeAsset(w http.ResponseWriter, r *http.Request, body []byte, contentType, etag string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}
