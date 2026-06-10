// Package httpapi stellt den HTTP-API-Layer von cliostore bereit.
//
// Implementiert sind: /api/v1/ping (offen), /api/v1/write-events und
// /api/v1/read-events (beide Bearer-Token-geschützt, ADR-008). Event-Listen
// werden als NDJSON ausgeliefert.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// ndjsonContentType ist der Content-Type für Newline-Delimited JSON.
const ndjsonContentType = "application/x-ndjson"

// Server kapselt Konfiguration, Storage und Router des HTTP-API-Layers.
type Server struct {
	cfg    config.Config
	store  *store.Store
	logger *slog.Logger
	mux    *http.ServeMux
}

// New erzeugt einen konfigurierten Server. Ist logger nil, wird der
// Default-Logger verwendet.
func New(cfg config.Config, st *store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:    cfg,
		store:  st,
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

	// Datenrouten sind durch das Bearer-Token geschützt (ADR-008).
	s.mux.HandleFunc("POST /api/v1/write-events", s.requireAuth(s.handleWriteEvents))
	s.mux.HandleFunc("POST /api/v1/read-events", s.requireAuth(s.handleReadEvents))
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeEventsRequest ist der Request-Body von /write-events.
type writeEventsRequest struct {
	Events []event.Candidate `json:"events"`
}

func (s *Server) handleWriteEvents(w http.ResponseWriter, r *http.Request) {
	var req writeEventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "events darf nicht leer sein")
		return
	}
	for i, c := range req.Events {
		if err := c.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "events["+strconv.Itoa(i)+"]: "+err.Error())
			return
		}
	}

	written, err := s.store.Append(req.Events)
	if err != nil {
		s.logger.Error("write-events fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim schreiben")
		return
	}

	writeNDJSON(w, s.logger, written)
}

// readEventsRequest ist der Request-Body von /read-events.
type readEventsRequest struct {
	Subject string `json:"subject"`
}

func (s *Server) handleReadEvents(w http.ResponseWriter, r *http.Request) {
	var req readEventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Subject == "" || req.Subject[0] != '/' {
		writeError(w, http.StatusBadRequest, "subject muss mit \"/\" beginnen")
		return
	}

	events, err := s.store.ReadSubject(req.Subject)
	if err != nil {
		s.logger.Error("read-events fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}

	writeNDJSON(w, s.logger, events)
}

// requireAuth umschließt einen Handler mit der Bearer-Token-Prüfung (ADR-008).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	expected := []byte("Bearer " + s.cfg.APIToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		// Konstante Laufzeit gegen Timing-Angriffe.
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("ungültiger request-body: " + err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeNDJSON schreibt eine Event-Liste als Newline-Delimited JSON.
func writeNDJSON(w http.ResponseWriter, logger *slog.Logger, events []event.Event) {
	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			// Header sind bereits gesendet; nur noch loggen.
			logger.Error("ndjson schreiben fehlgeschlagen", "err", err)
			return
		}
	}
}
