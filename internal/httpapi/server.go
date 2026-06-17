// Package httpapi stellt den HTTP-API-Layer von cliostore bereit.
//
// Der Layer bündelt:
//   - HTTP-Routing über einen http.ServeMux (siehe routes.go),
//   - Authentifizierung/Autorisierung per benannter API-Keys mit Scopes
//     (`Bearer kid.secret`, ADR-025; siehe auth_middleware.go),
//   - Observability: strukturiertes Request-Logging und Prometheus-Metriken
//     (siehe middleware.go),
//   - die eigentlichen Request-Handler (siehe handlers.go) sowie
//   - einheitliche Antwortformate: NDJSON für Event-Listen und
//     RFC-7807-Fehler (`application/problem+json`; siehe respond.go).
//
// Dieser Datei (server.go) gehören Aufbau und Lebenszyklus des Servers:
// der Server-Typ, seine Optionen und der Konstruktor.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/eventstats"
	"github.com/pblumer/clio/internal/metrics"
	"github.com/pblumer/clio/internal/pubsub"
	"github.com/pblumer/clio/internal/query"
	"github.com/pblumer/clio/internal/store"
)

// Server kapselt Konfiguration, Storage und Router des HTTP-API-Layers.
type Server struct {
	cfg             config.Config
	store           *store.Store
	broker          *pubsub.Broker
	metrics         *metrics.Metrics
	events          *eventstats.Histogram
	queryC          *query.Compiler
	logger          *slog.Logger
	mux             *http.ServeMux
	version         string
	startedAt       time.Time
	devMode         bool
	eventAuthorship bool

	bulkMu         sync.RWMutex
	bulkImportOpen bool
}

// Option konfiguriert optionale Server-Metadaten.
type Option func(*Server)

// WithBuildInfo setzt Build-Version und Startzeit für /api/v1/info.
func WithBuildInfo(version string, startedAt time.Time) Option {
	return func(s *Server) {
		if strings.TrimSpace(version) != "" {
			s.version = version
		}
		if !startedAt.IsZero() {
			s.startedAt = startedAt
		}
	}
}

// New erzeugt einen konfigurierten Server. Ist logger nil, wird der
// Default-Logger verwendet.
func New(cfg config.Config, st *store.Store, logger *slog.Logger, opts ...Option) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	qc, err := query.NewCompiler()
	if err != nil {
		// Statische Umgebung — sollte nicht fehlschlagen; run-query meldet sonst 500.
		logger.Error("query-compiler konnte nicht erstellt werden", "err", err)
	}

	now := time.Now().UTC()
	s := &Server{
		cfg:             cfg,
		store:           st,
		broker:          pubsub.New(),
		metrics:         metrics.New(),
		queryC:          qc,
		logger:          logger,
		mux:             http.NewServeMux(),
		version:         "dev",
		startedAt:       now,
		devMode:         cfg.DevMode,
		eventAuthorship: cfg.EventAuthorship,
		bulkImportOpen:  cfg.DevMode,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	// Eventmengen-Histogramm aus der bestehenden Historie aufbauen (nach
	// Event-Zeit), damit das Dashboard auch ohne neue Writes zeigt, wann wie
	// viele Events gesendet wurden. origin = früheste (erste) Eventzeit.
	var hist *eventstats.Histogram
	if err := st.ForEachEventTimeSource(func(t time.Time, source string) {
		if hist == nil {
			hist = eventstats.New(t)
		}
		hist.AddSource(1, t, source)
	}); err != nil {
		s.logger.Error("event-stats: seeding aus der historie fehlgeschlagen", "err", err)
	}
	if hist == nil {
		hist = eventstats.New(s.startedAt)
	}
	s.events = hist
	s.routes()
	return s
}

// Handler liefert den http.Handler des Servers, umschlossen von der
// Observability-Middleware (Request-Logging + Metriken).
func (s *Server) Handler() http.Handler {
	return s.instrument(s.mux)
}
