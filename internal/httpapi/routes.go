package httpapi

import (
	"net/http"

	"github.com/swaggest/swgui/v5emb"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/webui"
)

func (s *Server) routes() {
	// ping ist eine reine Erreichbarkeitsprüfung (Liveness) und bewusst
	// ohne Auth erreichbar — es gibt keine internen Daten preis.
	s.mux.HandleFunc("GET /api/v1/ping", s.handlePing)
	s.mux.HandleFunc("POST /api/v1/ping", s.handlePing)

	// Datenrouten sind scope-bewusst über den Schlüsselbund geschützt (ADR-025).
	// Lesende Routen verlangen `read`, schreibende `write`, Verwaltung `admin`.
	s.mux.HandleFunc("GET /api/v1/info", s.requireScope(auth.ScopeRead, s.handleInfo))
	s.mux.HandleFunc("GET /api/v1/event-stats", s.requireScope(auth.ScopeRead, s.handleEventStats))
	s.mux.HandleFunc("POST /api/v1/write-events", s.requireScope(auth.ScopeWrite, s.handleWriteEvents))
	s.mux.HandleFunc("POST /api/v1/read-events", s.requireScope(auth.ScopeRead, s.handleReadEvents))
	s.mux.HandleFunc("POST /api/v1/observe-events", s.requireScope(auth.ScopeRead, s.handleObserveEvents))
	s.mux.HandleFunc("POST /api/v1/run-query", s.requireScope(auth.ScopeRead, s.handleRunQuery))
	s.mux.HandleFunc("GET /api/v1/verify", s.requireScope(auth.ScopeRead, s.handleVerify))
	s.mux.HandleFunc("GET /api/v1/public-key", s.requireScope(auth.ScopeRead, s.handlePublicKey))
	s.mux.HandleFunc("GET /api/v1/read-subjects", s.requireScope(auth.ScopeRead, s.handleReadSubjects))
	s.mux.HandleFunc("GET /api/v1/read-event-types", s.requireScope(auth.ScopeRead, s.handleReadEventTypes))
	s.mux.HandleFunc("POST /api/v1/register-event-schema", s.requireScope(auth.ScopeWrite, s.handleRegisterEventSchema))
	s.mux.HandleFunc("GET /api/v1/read-event-schema", s.requireScope(auth.ScopeRead, s.handleReadEventSchema))

	// Schlüsselverwaltung zur Laufzeit (ADR-025) — ausschließlich Scope admin.
	s.mux.HandleFunc("POST /api/v1/keys", s.requireScope(auth.ScopeAdmin, s.handleCreateKey))
	s.mux.HandleFunc("GET /api/v1/keys", s.requireScope(auth.ScopeAdmin, s.handleListKeys))
	s.mux.HandleFunc("POST /api/v1/keys/{kid}/revoke", s.requireScope(auth.ScopeAdmin, s.handleRevokeKey))

	// Dev-Mode-only (ADR-022): destruktives Zurücksetzen der gesamten Datenbank
	// plus Bulk-Import-Fenster direkt nach Start/Reset.
	// Die Routen werden im Produktivbetrieb gar nicht erst registriert — ohne
	// CLIO_DEV_MODE liefern sie damit 404 statt nur 401. Scope: admin.
	if s.devMode {
		s.mux.HandleFunc("POST /api/v1/dev/reset-database", s.requireScope(auth.ScopeAdmin, s.handleDevReset))
		s.mux.HandleFunc("POST /api/v1/dev/bulk-import-events", s.requireScope(auth.ScopeAdmin, s.handleDevBulkImportEvents))
		s.mux.HandleFunc("POST /api/v1/dev/close-bulk-import", s.requireScope(auth.ScopeAdmin, s.handleDevCloseBulkImport))
	}

	// Prometheus-Metriken (ohne Auth, üblich für Scraping im internen Netz).
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Komfort-Leseroute: GET /api/v1/events/<subject> (Subject = Pfad). Optionen
	// als Query-Parameter (recursive, lowerBound, upperBound, type, watch).
	// `GET /api/v1/events` ohne Subject = Wurzel (alle Events).
	s.mux.HandleFunc("GET /api/v1/events", s.requireScope(auth.ScopeRead, s.handleEventsPath))
	s.mux.HandleFunc("GET /api/v1/events/{subject...}", s.requireScope(auth.ScopeRead, s.handleEventsPath))

	// Betriebs-Dashboard (ADR-020): statische, eingebettete Seite unter /ui plus
	// ausgelagerte Assets (z. B. /ui/css/dashboard.css) unter /ui/<pfad>.
	// Wie /docs bewusst ohne Auth (nicht sensibel); die Daten holt die Seite
	// clientseitig von /api/v1/info (Bearer-Token) und /metrics. Das nackte /ui/
	// leitet weiterhin auf die kanonische /ui um (im AssetHandler).
	s.mux.Handle("GET /ui", webui.Handler())
	s.mux.Handle("GET /ui/{path...}", webui.AssetHandler())

	// API-Doku: OpenAPI-Spec + interaktive UI. Bewusst ohne Auth (nicht
	// sensibel); „Try it out" nutzt das Bearer-Token, das der Nutzer eingibt.
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPISpec)
	s.mux.Handle("/docs/", v5emb.New("cliostore API", "/openapi.yaml", "/docs/"))
	s.mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
	})
}
