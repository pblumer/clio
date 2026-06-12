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
	"strings"
	"time"

	"github.com/swaggest/swgui/v5emb"

	"github.com/pblumer/clio/internal/apidocs"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/metrics"
	"github.com/pblumer/clio/internal/pubsub"
	"github.com/pblumer/clio/internal/store"
)

// ndjsonContentType ist der Content-Type für Newline-Delimited JSON.
const ndjsonContentType = "application/x-ndjson"

// Server kapselt Konfiguration, Storage und Router des HTTP-API-Layers.
type Server struct {
	cfg       config.Config
	store     *store.Store
	broker    *pubsub.Broker
	metrics   *metrics.Metrics
	logger    *slog.Logger
	mux       *http.ServeMux
	version   string
	startedAt time.Time
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
	s := &Server{
		cfg:       cfg,
		store:     st,
		broker:    pubsub.New(),
		metrics:   metrics.New(),
		logger:    logger,
		mux:       http.NewServeMux(),
		version:   "dev",
		startedAt: time.Now().UTC(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	s.routes()
	return s
}

// Handler liefert den http.Handler des Servers, umschlossen von der
// Observability-Middleware (Request-Logging + Metriken).
func (s *Server) Handler() http.Handler {
	return s.instrument(s.mux)
}

// statusRecorder fängt den Status-Code (und reicht http.Flusher fürs Streaming
// durch), um Anfragen instrumentieren zu können.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// instrument loggt jede Anfrage strukturiert und verbucht sie in den Metriken.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		dur := time.Since(start)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		s.metrics.ObserveRequest(r.Method, route, rec.status, dur)
		s.logger.Info("request",
			"method", r.Method,
			"route", route,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", float64(dur.Microseconds())/1000,
		)
	})
}

func (s *Server) routes() {
	// ping ist eine reine Erreichbarkeitsprüfung (Liveness) und bewusst
	// ohne Auth erreichbar — es gibt keine internen Daten preis.
	s.mux.HandleFunc("GET /api/v1/ping", s.handlePing)
	s.mux.HandleFunc("POST /api/v1/ping", s.handlePing)

	// Datenrouten sind durch das Bearer-Token geschützt (ADR-008).
	s.mux.HandleFunc("GET /api/v1/info", s.requireAuth(s.handleInfo))
	s.mux.HandleFunc("POST /api/v1/write-events", s.requireAuth(s.handleWriteEvents))
	s.mux.HandleFunc("POST /api/v1/read-events", s.requireAuth(s.handleReadEvents))
	s.mux.HandleFunc("POST /api/v1/observe-events", s.requireAuth(s.handleObserveEvents))
	s.mux.HandleFunc("GET /api/v1/verify", s.requireAuth(s.handleVerify))
	s.mux.HandleFunc("GET /api/v1/read-event-types", s.requireAuth(s.handleReadEventTypes))
	s.mux.HandleFunc("POST /api/v1/register-event-schema", s.requireAuth(s.handleRegisterEventSchema))
	s.mux.HandleFunc("GET /api/v1/read-event-schema", s.requireAuth(s.handleReadEventSchema))

	// Prometheus-Metriken (ohne Auth, üblich für Scraping im internen Netz).
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Komfort-Leseroute: GET /api/v1/events/<subject> (Subject = Pfad). Optionen
	// als Query-Parameter (recursive, lowerBound, upperBound, type, watch).
	// `GET /api/v1/events` ohne Subject = Wurzel (alle Events).
	s.mux.HandleFunc("GET /api/v1/events", s.requireAuth(s.handleEventsPath))
	s.mux.HandleFunc("GET /api/v1/events/{subject...}", s.requireAuth(s.handleEventsPath))

	// API-Doku: OpenAPI-Spec + interaktive UI. Bewusst ohne Auth (nicht
	// sensibel); „Try it out" nutzt das Bearer-Token, das der Nutzer eingibt.
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPISpec)
	s.mux.Handle("/docs/", v5emb.New("cliostore API", "/openapi.yaml", "/docs/"))
	s.mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
	})
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(apidocs.Spec)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleInfo liefert Laufzeit-Infos (Version, Uptime, Startzeit) plus
// grundlegende Store-Infos für Diagnose und Deploy-Verifikation.
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.Count()
	if err != nil {
		s.logger.Error("info: events zählen fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}

	now := time.Now().UTC()
	uptime := now.Sub(s.startedAt)
	if uptime < 0 {
		uptime = 0
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "cliostore",
		"version":         s.version,
		"startedAt":       s.startedAt.Format(time.RFC3339Nano),
		"uptimeSeconds":   int64(uptime.Seconds()),
		"serverTime":      now.Format(time.RFC3339Nano),
		"eventsTotal":     count,
		"syncMode":        s.cfg.Sync,
		"httpListenAddr":  s.cfg.Addr,
		"databaseFilePath": s.cfg.DBPath,
	})
}

// handleReadEventTypes liefert alle bisher geschriebenen Event-Typen als NDJSON
// ({"type":...,"count":...} pro Zeile).
func (s *Server) handleReadEventTypes(w http.ResponseWriter, r *http.Request) {
	types, err := s.store.EventTypes()
	if err != nil {
		s.logger.Error("read-event-types fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, t := range types {
		if err := enc.Encode(t); err != nil {
			s.logger.Error("ndjson schreiben fehlgeschlagen", "err", err)
			return
		}
	}
}

// registerEventSchemaRequest ist der Body von /register-event-schema.
type registerEventSchemaRequest struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

func (s *Server) handleRegisterEventSchema(w http.ResponseWriter, r *http.Request) {
	var req registerEventSchemaRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		writeError(w, http.StatusBadRequest, "type ist pflicht")
		return
	}
	if len(req.Schema) == 0 {
		writeError(w, http.StatusBadRequest, "schema ist pflicht")
		return
	}

	err := s.store.RegisterSchema(req.Type, req.Schema)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"type": req.Type, "status": "registered"})
	case errors.Is(err, store.ErrSchemaExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrSchemaValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.logger.Error("register-event-schema fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim registrieren")
	}
}

func (s *Server) handleReadEventSchema(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	if typ == "" {
		writeError(w, http.StatusBadRequest, "query-parameter type ist pflicht")
		return
	}
	schema, found, err := s.store.SchemaFor(typ)
	if err != nil {
		s.logger.Error("read-event-schema fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "für diesen typ ist kein schema registriert")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"type": typ, "schema": schema})
}

// handleMetrics liefert die Metriken im Prometheus-Textformat.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.Count()
	if err != nil {
		s.logger.Error("events zählen fehlgeschlagen", "err", err)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.Write(w, metrics.Gauges{
		ActiveObservers: s.broker.SubscriberCount(),
		EventsTotal:     count,
	})
}

// handleVerify rechnet die Hash-Kette nach und meldet, ob die Historie
// unverändert ist. Eine erkannte Manipulation ergibt HTTP 200 mit ok=false
// (die Prüfung selbst war erfolgreich) — erst ein interner Fehler ergibt 500.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	res, err := s.store.Verify()
	if err != nil {
		s.logger.Error("verify fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler bei der prüfung")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// preconditionWire ist die Drahtdarstellung einer Precondition im
// Request-Body: {"type": "...", "payload": {"subject": "...", "eventId": "..."}}.
type preconditionWire struct {
	Type    string `json:"type"`
	Payload struct {
		Subject string `json:"subject"`
		EventID string `json:"eventId"`
	} `json:"payload"`
}

// writeEventsRequest ist der Request-Body von /write-events.
type writeEventsRequest struct {
	Events        []event.Candidate  `json:"events"`
	Preconditions []preconditionWire `json:"preconditions"`
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

	preconditions, err := parsePreconditions(req.Preconditions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	written, err := s.store.Append(req.Events, preconditions)
	if err != nil {
		if errors.Is(err, store.ErrPreconditionFailed) {
			s.metrics.IncPreconditionFailure()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, store.ErrSchemaValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("write-events fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim schreiben")
		return
	}

	s.metrics.AddEventsWritten(len(written))

	// Live-Observer benachrichtigen (nach erfolgreichem, committetem Write).
	s.broker.Publish(written)

	writeNDJSON(w, s.logger, written)
}

// parsePreconditions validiert die Drahtdarstellung und übersetzt sie in
// store.Precondition. Format-/Typfehler ergeben 400 (kein 409).
func parsePreconditions(wire []preconditionWire) ([]store.Precondition, error) {
	if len(wire) == 0 {
		return nil, nil
	}
	out := make([]store.Precondition, 0, len(wire))
	for i, p := range wire {
		prefix := "preconditions[" + strconv.Itoa(i) + "]: "
		if p.Payload.Subject == "" || p.Payload.Subject[0] != '/' {
			return nil, errors.New(prefix + "subject muss mit \"/\" beginnen")
		}
		switch p.Type {
		case store.PreconditionSubjectPristine:
		case store.PreconditionSubjectOnEventID:
			if _, err := strconv.ParseUint(p.Payload.EventID, 10, 64); err != nil {
				return nil, errors.New(prefix + "eventId muss eine nicht-negative ganze Zahl sein")
			}
		default:
			return nil, errors.New(prefix + "unbekannter typ " + strconv.Quote(p.Type))
		}
		out = append(out, store.Precondition{
			Type:    p.Type,
			Subject: p.Payload.Subject,
			EventID: p.Payload.EventID,
		})
	}
	return out, nil
}

// readEventsRequest ist der Request-Body von /read-events. lowerBound und
// upperBound sind optionale, inklusive Event-ID-Grenzen (CloudEvents-IDs sind
// Strings, hier eine nicht-negative ganze Zahl).
type readEventsRequest struct {
	Subject    string   `json:"subject"`
	Recursive  bool     `json:"recursive"`
	LowerBound string   `json:"lowerBound"`
	UpperBound string   `json:"upperBound"`
	Types      []string `json:"types"`
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

	lower, err := parseBound(req.LowerBound, "lowerBound")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	upper, err := parseBound(req.UpperBound, "upperBound")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if lower != 0 && upper != 0 && lower > upper {
		writeError(w, http.StatusBadRequest, "lowerBound darf nicht größer als upperBound sein")
		return
	}
	if err := validateTypes(req.Types); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.doRead(w, req.Subject, req.Recursive, store.ReadOptions{
		LowerBound: lower,
		UpperBound: upper,
		Types:      req.Types,
	})
}

// doRead liest Events und schreibt sie als NDJSON (oder 500 bei Fehler).
// Gemeinsamer Kern von read-events (POST) und der GET-Pfad-Route.
func (s *Server) doRead(w http.ResponseWriter, subject string, recursive bool, opts store.ReadOptions) {
	events, err := s.store.Read(subject, recursive, opts)
	if err != nil {
		s.logger.Error("read fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	writeNDJSON(w, s.logger, events)
}

// validateTypes stellt sicher, dass jeder angegebene Typ-Filter nicht leer ist.
func validateTypes(types []string) error {
	for i, t := range types {
		if strings.TrimSpace(t) == "" {
			return errors.New("types[" + strconv.Itoa(i) + "] darf nicht leer sein")
		}
	}
	return nil
}

// typeSet baut ein Lookup-Set für den Live-Typ-Filter; nil bei leerer Liste.
func typeSet(types []string) map[string]struct{} {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return set
}

// handleEventsPath bedient die Komfort-Leseroute GET /api/v1/events/<subject>.
// Das Subject wird aus dem Pfad gebildet, Optionen kommen als Query-Parameter:
//   - recursive=true|false (Default true: Eltern-Pfade liefern alles darunter)
//   - lowerBound, upperBound (inklusive Event-ID-Grenzen)
//   - type=... (wiederholbar) — Filter nach Event-Typ
//   - watch=true — Verbindung offen halten und live nachliefern (wie observe)
func (s *Server) handleEventsPath(w http.ResponseWriter, r *http.Request) {
	// Subject aus dem Pfad: "books/42" -> "/books/42"; leer -> "/".
	subject := "/" + strings.TrimSuffix(r.PathValue("subject"), "/")

	q := r.URL.Query()

	recursive := true
	if v := q.Get("recursive"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "recursive muss true oder false sein")
			return
		}
		recursive = b
	}

	lower, err := parseBound(q.Get("lowerBound"), "lowerBound")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	upper, err := parseBound(q.Get("upperBound"), "upperBound")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if lower != 0 && upper != 0 && lower > upper {
		writeError(w, http.StatusBadRequest, "lowerBound darf nicht größer als upperBound sein")
		return
	}

	types := q["type"]
	if err := validateTypes(types); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	watch := false
	if v := q.Get("watch"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "watch muss true oder false sein")
			return
		}
		watch = b
	}

	if watch {
		s.doObserve(w, r, subject, recursive, lower, types)
		return
	}
	s.doRead(w, subject, recursive, store.ReadOptions{LowerBound: lower, UpperBound: upper, Types: types})
}

// parseBound parst eine optionale ID-Grenze. Leer bedeutet „keine Grenze" (0).
func parseBound(v, name string) (uint64, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, errors.New(name + " muss eine nicht-negative ganze Zahl sein")
	}
	return n, nil
}

// observeEventsRequest ist der Request-Body von /observe-events.
type observeEventsRequest struct {
	Subject    string   `json:"subject"`
	Recursive  bool     `json:"recursive"`
	LowerBound string   `json:"lowerBound"`
	Types      []string `json:"types"`
}

// handleObserveEvents liefert zuerst die passende History und hält die
// Verbindung anschließend offen, um neue Events live nachzuliefern (Stufe 2).
// Reconnect erfolgt clientseitig über lowerBound.
func (s *Server) handleObserveEvents(w http.ResponseWriter, r *http.Request) {
	var req observeEventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Subject == "" || req.Subject[0] != '/' {
		writeError(w, http.StatusBadRequest, "subject muss mit \"/\" beginnen")
		return
	}
	lower, err := parseBound(req.LowerBound, "lowerBound")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTypes(req.Types); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.doObserve(w, r, req.Subject, req.Recursive, lower, req.Types)
}

// doObserve liefert zuerst die passende History und hält die Verbindung dann
// offen für Live-Events. Gemeinsamer Kern von observe-events (POST) und der
// GET-Pfad-Route mit ?watch=true.
func (s *Server) doObserve(w http.ResponseWriter, r *http.Request, subject string, recursive bool, lower uint64, types []string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming nicht unterstützt")
		return
	}

	// Zuerst abonnieren, dann History lesen: so geht kein Event verloren, das
	// zwischen History-Snapshot und Live-Phase geschrieben wird. Doppelte
	// werden über die ID (lastID) verworfen.
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	typeFilter := typeSet(types)
	history, err := s.store.Read(subject, recursive, store.ReadOptions{LowerBound: lower, Types: types})
	if err != nil {
		s.logger.Error("observe history fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}

	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)

	// lastID = höchste bereits ausgelieferte ID. Initial untere Grenze − 1,
	// damit Live-Events ab lowerBound und nur neuer als die History kommen.
	var lastID uint64
	if lower > 0 {
		lastID = lower - 1
	}
	for _, ev := range history {
		if err := enc.Encode(ev); err != nil {
			return
		}
		if id, perr := strconv.ParseUint(ev.ID, 10, 64); perr == nil && id > lastID {
			lastID = id
		}
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Lost:
			return
		case ev := <-sub.Events:
			id, perr := strconv.ParseUint(ev.ID, 10, 64)
			if perr != nil || id <= lastID {
				continue
			}
			if !store.MatchSubject(ev.Subject, subject, recursive) {
				continue
			}
			if typeFilter != nil {
				if _, ok := typeFilter[ev.Type]; !ok {
					continue
				}
			}
			if err := enc.Encode(ev); err != nil {
				return
			}
			flusher.Flush()
			lastID = id
		}
	}
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
