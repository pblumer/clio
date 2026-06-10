// Package httpapi stellt den HTTP-API-Layer von cliostore bereit.
//
// In Stufe 0 (Walking Skeleton) ist nur /api/v1/ping implementiert. Die
// Routen für write-events und read-events folgen, sobald der Storage-Layer
// (bbolt, siehe ADR-006) angebunden ist.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/pblumer/clio/internal/config"
)

// Server kapselt Konfiguration und Router des HTTP-API-Layers.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	mux    *http.ServeMux
}

// New erzeugt einen konfigurierten Server. Ist logger nil, wird der
// Default-Logger verwendet.
func New(cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:    cfg,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler liefert den http.Handler des Servers (nützlich für Tests).
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	// ping ist eine reine Erreichbarkeitsprüfung (Liveness) und bewusst
	// ohne Auth erreichbar — es gibt keine internen Daten preis.
	s.mux.HandleFunc("GET /api/v1/ping", s.handlePing)
	s.mux.HandleFunc("POST /api/v1/ping", s.handlePing)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// requireAuth umschließt einen Handler mit der Bearer-Token-Prüfung nach
// ADR-008. Wird ab den geschützten Routen (write/read) verwendet.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	expected := []byte("Bearer " + s.cfg.APIToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		// Konstante Laufzeit gegen Timing-Angriffe.
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized",
			})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
