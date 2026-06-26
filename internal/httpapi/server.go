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
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/clio/internal/activity"
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

	// activity ist die in-memory Presence-/Aktivitäts-Registry (ADR-030).
	activity *activity.Registry
	// stateCache memoisiert gefaltete Subject-Zustände (ADR-040): ephemerer LRU,
	// lazy-inkrementell fortgeschrieben, beim Dev-Reset geleert.
	stateCache *stateCache
	// deniedThrottle begrenzt access-denied-Events je kid (ADR-030, gegen Flutung).
	deniedMu       sync.Mutex
	deniedLastSeen map[string]time.Time

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
		activity:        activity.New(cfg.PresenceWindow),
		stateCache:      newStateCache(defaultStateCacheSize),
		deniedLastSeen:  make(map[string]time.Time),
		bulkImportOpen:  cfg.DevMode,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	// Eventmengen-Histogramm für das Dashboard. Der Origin (Beginn von Bucket 0)
	// ist die Zeit des ersten Events — günstig per FirstEventTime (O(1)) ermittelt,
	// damit historische Events korrekt über die Zeitachse verteilt werden.
	//
	// WICHTIG: Das eigentliche Befüllen aus der Historie scannt JEDES Event und
	// lief früher SYNCHRON im Konstruktor — bei Millionen Events blockierte das den
	// Start des HTTP-Listeners um Sekunden (→ 502 vom vorgelagerten Proxy bei jedem
	// (Neu-)Start). Es läuft jetzt asynchron im Hintergrund: der Server ist sofort
	// erreichbar, der Eventstrom-Chart füllt sich nach.
	origin := s.startedAt
	if t, ok, err := st.FirstEventTime(); err != nil {
		s.logger.Error("event-stats: erste eventzeit ermitteln fehlgeschlagen", "err", err)
	} else if ok {
		origin = t
	}
	s.events = eventstats.New(origin)

	// Saubere Grenze gegen Doppelzählung: nur Events bis zum aktuell höchsten Seq
	// werden im Hintergrund geseedet; alle danach geschriebenen Events zählt der
	// Write-Pfad (recordEventStats) selbst. Da der Listener erst nach New() startet,
	// sind zur Aufnahme dieser Grenze noch keine neuen Writes dieses Prozesses möglich.
	seedUpTo, err := st.Count()
	if err != nil {
		s.logger.Error("event-stats: seed-grenze ermitteln fehlgeschlagen", "err", err)
	}
	if seedUpTo > 0 {
		go func() {
			if err := st.ForEachEventTimeSource(seedUpTo, func(t time.Time, source string) {
				s.events.AddSource(1, t, source)
			}); err != nil {
				s.logger.Error("event-stats: seeding aus der historie fehlgeschlagen", "err", err)
			}
		}()
	}

	s.routes()
	return s
}

// Handler liefert den http.Handler des Servers, umschlossen von der
// Observability-Middleware (Request-Logging + Metriken).
func (s *Server) Handler() http.Handler {
	return s.instrument(s.mux)
}

// StartBackground startet langlaufende Hintergrundaufgaben des API-Layers und
// beendet sie sauber, wenn ctx abgebrochen wird (Graceful Shutdown). Aktuell:
// der Presence-Sweeper (ADR-030), der abgelaufene Sessions schließt und — bei
// aktiviertem CLIO_AUTH_EVENTS — session-ended-Events schreibt. Wird aus main
// aufgerufen; Tests, die nur Handler() nutzen, starten bewusst keinen Sweeper.
func (s *Server) StartBackground(ctx context.Context) {
	// Tick zweimal pro Fenster, mindestens jede Sekunde — ein Fenster wird so um
	// höchstens ~Fenster/2 überschritten, bevor die Session als beendet gilt.
	interval := s.cfg.PresenceWindow / 2
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for _, e := range s.activity.Sweep(time.Now().UTC()) {
					s.emitSessionEnded(e)
				}
			}
		}
	}()
}
