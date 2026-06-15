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
	"github.com/pblumer/clio/internal/eventstats"
	"github.com/pblumer/clio/internal/metrics"
	"github.com/pblumer/clio/internal/pubsub"
	"github.com/pblumer/clio/internal/query"
	"github.com/pblumer/clio/internal/store"
	"github.com/pblumer/clio/internal/webui"
)

// ndjsonContentType ist der Content-Type für Newline-Delimited JSON.
const ndjsonContentType = "application/x-ndjson"

// Server kapselt Konfiguration, Storage und Router des HTTP-API-Layers.
type Server struct {
	cfg       config.Config
	store     *store.Store
	broker    *pubsub.Broker
	metrics   *metrics.Metrics
	events    *eventstats.Histogram
	queryC    *query.Compiler
	logger    *slog.Logger
	mux       *http.ServeMux
	version   string
	startedAt time.Time
	devMode   bool
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

	s := &Server{
		cfg:       cfg,
		store:     st,
		broker:    pubsub.New(),
		metrics:   metrics.New(),
		queryC:    qc,
		logger:    logger,
		mux:       http.NewServeMux(),
		version:   "dev",
		startedAt: time.Now().UTC(),
		devMode:   cfg.DevMode,
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
	if err := st.ForEachEventTime(func(t time.Time) {
		if hist == nil {
			hist = eventstats.New(t)
		}
		hist.Add(1, t)
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

		// Default-Header: Antworten enthalten dynamische Daten und sollen nicht
		// gecacht werden (Swiss-Guidelines Quick Win, ADR-019). Handler können
		// dies bei Bedarf überschreiben (z. B. statische Doc-Assets).
		rec.Header().Set("Cache-Control", "no-store")

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
	s.mux.HandleFunc("GET /api/v1/event-stats", s.requireAuth(s.handleEventStats))
	s.mux.HandleFunc("POST /api/v1/write-events", s.requireAuth(s.handleWriteEvents))
	s.mux.HandleFunc("POST /api/v1/read-events", s.requireAuth(s.handleReadEvents))
	s.mux.HandleFunc("POST /api/v1/observe-events", s.requireAuth(s.handleObserveEvents))
	s.mux.HandleFunc("POST /api/v1/run-query", s.requireAuth(s.handleRunQuery))
	s.mux.HandleFunc("GET /api/v1/verify", s.requireAuth(s.handleVerify))
	s.mux.HandleFunc("GET /api/v1/public-key", s.requireAuth(s.handlePublicKey))
	s.mux.HandleFunc("GET /api/v1/read-subjects", s.requireAuth(s.handleReadSubjects))
	s.mux.HandleFunc("GET /api/v1/read-event-types", s.requireAuth(s.handleReadEventTypes))
	s.mux.HandleFunc("POST /api/v1/register-event-schema", s.requireAuth(s.handleRegisterEventSchema))
	s.mux.HandleFunc("GET /api/v1/read-event-schema", s.requireAuth(s.handleReadEventSchema))

	// Dev-Mode-only (ADR-022): destruktives Zurücksetzen der gesamten Datenbank.
	// Die Route wird im Produktivbetrieb gar nicht erst registriert — ohne
	// CLIO_DEV_MODE liefert sie damit 404 statt nur 401. Bearer-Token-geschützt.
	if s.devMode {
		s.mux.HandleFunc("POST /api/v1/dev/reset-database", s.requireAuth(s.handleDevReset))
	}

	// Prometheus-Metriken (ohne Auth, üblich für Scraping im internen Netz).
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Komfort-Leseroute: GET /api/v1/events/<subject> (Subject = Pfad). Optionen
	// als Query-Parameter (recursive, lowerBound, upperBound, type, watch).
	// `GET /api/v1/events` ohne Subject = Wurzel (alle Events).
	s.mux.HandleFunc("GET /api/v1/events", s.requireAuth(s.handleEventsPath))
	s.mux.HandleFunc("GET /api/v1/events/{subject...}", s.requireAuth(s.handleEventsPath))

	// Betriebs-Dashboard (ADR-020): statische, eingebettete Seite unter /ui.
	// Wie /docs bewusst ohne Auth (nicht sensibel); die Daten holt die Seite
	// clientseitig von /api/v1/info (Bearer-Token) und /metrics.
	s.mux.Handle("GET /ui", webui.Handler())
	s.mux.HandleFunc("GET /ui/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusMovedPermanently)
	})

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
		"name":             "cliostore",
		"version":          s.version,
		"startedAt":        s.startedAt.Format(time.RFC3339Nano),
		"uptimeSeconds":    int64(uptime.Seconds()),
		"serverTime":       now.Format(time.RFC3339Nano),
		"eventsTotal":      count,
		"syncMode":         s.cfg.Sync,
		"httpListenAddr":   s.cfg.Addr,
		"databaseFilePath": s.cfg.DBPath,
		"devMode":          s.devMode,
	})
}

// handleDevReset setzt die gesamte Datenbank zurück (Tabula rasa) und meldet, wie
// viele Events dabei „verglüht" sind. Diese Route existiert NUR im Dev-Mode
// (CLIO_DEV_MODE, ADR-022); im Produktivbetrieb ist sie nicht registriert. Sie
// ist trotzdem zusätzlich Bearer-Token-geschützt (Defense in Depth).
//
// Hinweis: Bereits laufende Observer-Streams werden nicht aktiv getrennt; da die
// Sequenz wieder bei 1 beginnt, liegen neue IDs unter ihrem zuletzt gesehenen
// Stand und sie liefern erst nach einem Reconnect wieder. Für ein Dev-Werkzeug
// ist das akzeptabel — das Dashboard verbindet seinen Stream beim nächsten
// „Verbinden" ohnehin neu.
func (s *Server) handleDevReset(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.store.Reset()
	if err != nil {
		s.logger.Error("dev-reset fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim zurücksetzen")
		return
	}

	now := time.Now().UTC()
	// Eventstrom-Histogramm zurücksetzen, damit der Chart ebenfalls bei null
	// startet (origin = jetzt).
	s.events.Reset(now)
	s.logger.Warn("datenbank zurückgesetzt (dev-mode)", "deletedEvents", deleted)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "tabula-rasa",
		"deletedEvents": deleted,
		"resetAt":       now.Format(time.RFC3339Nano),
		"message":       "Supernova! Die Historie ist zu Sternenstaub zerfallen.",
	})
}

// handleEventStats liefert das Histogramm der Events über die Zeit (nach
// Event-Zeit; beim Start aus der Historie aufgebaut): Startzeitpunkt,
// Bucket-Breite (Sekunden) und die Bucket-Zähler. So kann das /ui-Dashboard die
// Eventmengen über die Zeitachse zeichnen, ohne die gesamte Historie zu streamen.
func (s *Server) handleEventStats(w http.ResponseWriter, r *http.Request) {
	snap := s.events.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"start":         snap.Origin.Format(time.RFC3339Nano),
		"bucketSeconds": snap.Width.Seconds(),
		"counts":        snap.Counts,
		"total":         snap.Total,
		"serverTime":    time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// handleReadSubjects liefert alle bisher beschriebenen Subjects (Streams) als
// NDJSON ({"subject":...,"count":...} pro Zeile), sortiert. Optionaler
// Query-Parameter `prefix` schränkt auf den rekursiven Scope eines Pfads ein
// (z. B. ?prefix=/books). Mit `tree=true` wird stattdessen ein hierarchischer
// Baum als einzelnes JSON-Objekt zurückgegeben.
func (s *Server) handleReadSubjects(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	if prefix != "" && prefix[0] != '/' {
		writeError(w, http.StatusBadRequest, "prefix muss mit \"/\" beginnen")
		return
	}
	subjects, err := s.store.Subjects(prefix)
	if err != nil {
		s.logger.Error("read-subjects fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	if r.URL.Query().Get("tree") == "true" {
		root := prefix
		if root == "" {
			root = "/"
		}
		writeJSON(w, http.StatusOK, buildSubjectTree(subjects, root))
		return
	}
	writeNDJSON(w, s.logger, subjects)
}

// subjectTreeNode ist ein Knoten im Subject-Baum. `count` sind die Events exakt
// auf diesem Subject (0 für reine Zwischenknoten), `total` die aggregierte
// Anzahl im gesamten Teilbaum. `children` ist nie null (leeres Array bei
// Blättern).
type subjectTreeNode struct {
	Subject  string             `json:"subject"`
	Count    uint64             `json:"count"`
	Total    uint64             `json:"total"`
	Children []*subjectTreeNode `json:"children"`
}

func newSubjectTreeNode(subject string) *subjectTreeNode {
	return &subjectTreeNode{Subject: subject, Children: []*subjectTreeNode{}}
}

// buildSubjectTree formt die flache, alphabetisch sortierte Subject-Liste in
// einen hierarchischen Baum mit Wurzel root ("/" oder ein prefix). Zwischen-
// segmente, die selbst kein Subject sind (z. B. "/books" bei vorhandenem
// "/books/42"), entstehen als Knoten mit count=0. Da die Eingabe sortiert ist,
// erscheinen Kinder in sortierter Reihenfolge.
func buildSubjectTree(subjects []store.SubjectInfo, root string) *subjectTreeNode {
	rootNode := newSubjectTreeNode(root)
	nodes := map[string]*subjectTreeNode{root: rootNode}

	for _, si := range subjects {
		var rel string
		switch {
		case si.Subject == root:
			rel = ""
		case root == "/":
			rel = strings.TrimPrefix(si.Subject, "/")
		default:
			rel = strings.TrimPrefix(si.Subject, root+"/")
		}

		cur, curPath := rootNode, root
		for _, seg := range strings.Split(rel, "/") {
			if seg == "" {
				continue
			}
			childPath := curPath + "/" + seg
			if curPath == "/" {
				childPath = "/" + seg
			}
			child := nodes[childPath]
			if child == nil {
				child = newSubjectTreeNode(childPath)
				nodes[childPath] = child
				cur.Children = append(cur.Children, child)
			}
			cur, curPath = child, childPath
		}
		cur.Count = si.Count
	}

	computeSubtreeTotals(rootNode)
	return rootNode
}

// computeSubtreeTotals summiert die Events je Teilbaum (Post-Order) und liefert
// die Summe des Teilbaums.
func computeSubtreeTotals(n *subjectTreeNode) uint64 {
	sum := n.Count
	for _, c := range n.Children {
		sum += computeSubtreeTotals(c)
	}
	n.Total = sum
	return sum
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

// handlePublicKey liefert den öffentlichen Signaturschlüssel (base64), mit dem
// Clients die Event-Signaturen selbst prüfen können. 404, wenn nicht signiert
// wird.
func (s *Server) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	pub, ok := s.store.PublicKey()
	if !ok {
		writeError(w, http.StatusNotFound, "signieren ist nicht aktiviert")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"algorithm": "ed25519",
		"publicKey": store.EncodePublicKey(pub),
	})
}

// handleMetrics liefert die Metriken im Prometheus-Textformat.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.Count()
	if err != nil {
		s.logger.Error("events zählen fehlgeschlagen", "err", err)
	}
	size, err := s.store.Size()
	if err != nil {
		s.logger.Error("db-größe ermitteln fehlgeschlagen", "err", err)
		size = -1
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.Write(w, metrics.Gauges{
		ActiveObservers: s.broker.SubscriberCount(),
		EventsTotal:     count,
		DBSizeBytes:     size,
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
// Request-Body: {"type": "...", "payload": {"subject": "...", ...}}.
// recursive/where gelten nur für die Query-Preconditions.
type preconditionWire struct {
	Type    string `json:"type"`
	Payload struct {
		Subject   string `json:"subject"`
		EventID   string `json:"eventId"`
		Recursive bool   `json:"recursive"`
		Where     string `json:"where"`
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

	preconditions, err := s.parsePreconditions(req.Preconditions)
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
	s.events.Add(len(written), time.Now().UTC())

	// Live-Observer benachrichtigen (nach erfolgreichem, committetem Write).
	s.broker.Publish(written)

	writeNDJSON(w, s.logger, written)
}

// parsePreconditions validiert die Drahtdarstellung und übersetzt sie in
// store.Precondition. Format-/Typfehler (inkl. ungültiger CEL-Ausdruck) ergeben
// 400 (kein 409).
func (s *Server) parsePreconditions(wire []preconditionWire) ([]store.Precondition, error) {
	if len(wire) == 0 {
		return nil, nil
	}
	out := make([]store.Precondition, 0, len(wire))
	for i, p := range wire {
		prefix := "preconditions[" + strconv.Itoa(i) + "]: "
		if p.Payload.Subject == "" || p.Payload.Subject[0] != '/' {
			return nil, errors.New(prefix + "subject muss mit \"/\" beginnen")
		}
		pc := store.Precondition{
			Type:      p.Type,
			Subject:   p.Payload.Subject,
			EventID:   p.Payload.EventID,
			Recursive: p.Payload.Recursive,
		}
		switch p.Type {
		case store.PreconditionSubjectPristine:
		case store.PreconditionSubjectOnEventID:
			if _, err := strconv.ParseUint(p.Payload.EventID, 10, 64); err != nil {
				return nil, errors.New(prefix + "eventId muss eine nicht-negative ganze Zahl sein")
			}
		case store.PreconditionQueryResultEmpty, store.PreconditionQueryResultNonEmpty:
			if strings.TrimSpace(p.Payload.Where) != "" {
				if s.queryC == nil {
					return nil, errors.New(prefix + "abfrage-engine nicht verfügbar")
				}
				pred, err := s.queryC.Compile(p.Payload.Where)
				if err != nil {
					return nil, errors.New(prefix + "where: " + err.Error())
				}
				pc.Predicate = pred
			}
		default:
			return nil, errors.New(prefix + "unbekannter typ " + strconv.Quote(p.Type))
		}
		out = append(out, pc)
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

// runQueryRequest ist der Body von /run-query (CEL-basierte Abfrage, ADR-017).
type runQueryRequest struct {
	Subject    string   `json:"subject"`
	Recursive  bool     `json:"recursive"`
	Where      string   `json:"where"` // CEL-Prädikat; leer = alle im Scope
	LowerBound string   `json:"lowerBound"`
	UpperBound string   `json:"upperBound"`
	Limit      int      `json:"limit"`  // 0 = unbegrenzt
	Select     []string `json:"select"` // Feldpfade für Projektion; leer = volles Event
}

// handleRunQuery liest die Events eines Scopes und filtert sie mit einem
// CEL-Prädikat (`where`). Ergebnis als NDJSON. Auswertungsfehler eines einzelnen
// Events (z. B. Zugriff auf ein fehlendes data-Feld ohne has()) gelten als
// „kein Treffer".
func (s *Server) handleRunQuery(w http.ResponseWriter, r *http.Request) {
	if s.queryC == nil {
		writeError(w, http.StatusInternalServerError, "abfrage-engine nicht verfügbar")
		return
	}
	var req runQueryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Subject == "" || req.Subject[0] != '/' {
		writeError(w, http.StatusBadRequest, "subject muss mit \"/\" beginnen")
		return
	}
	if req.Limit < 0 {
		writeError(w, http.StatusBadRequest, "limit darf nicht negativ sein")
		return
	}
	if err := query.ValidateFields(req.Select); err != nil {
		writeError(w, http.StatusBadRequest, "select: "+err.Error())
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

	var pred *query.Predicate
	if strings.TrimSpace(req.Where) != "" {
		p, err := s.queryC.Compile(req.Where)
		if err != nil {
			writeError(w, http.StatusBadRequest, "where: "+err.Error())
			return
		}
		pred = p
	}

	opts := store.ReadOptions{LowerBound: lower, UpperBound: upper}
	var result []event.Event

	// collect wendet das Prädikat an und sammelt Treffer bis zum Limit. Rückgabe
	// true = weiter scannen, false = genug (Limit erreicht).
	collect := func(ev event.Event) bool {
		if pred != nil {
			ok, err := pred.Eval(ev)
			if err != nil || !ok {
				return true
			}
		}
		result = append(result, ev)
		return req.Limit == 0 || len(result) < req.Limit
	}

	// Typ-Constraint aus dem Prädikat ableiten: Schränkt es den event.type
	// zwingend ein, laden wir nur die Events dieser Typen über den Typ-Index —
	// statt den ganzen Scope zu scannen (ADR-021).
	var reqTypes []string
	typeBounded := false
	if pred != nil {
		reqTypes, typeBounded = pred.RequiredTypes()
	}

	var scanErr error
	switch {
	case typeBounded && len(reqTypes) == 0:
		// Kein Typ kann das Prädikat erfüllen → leeres Ergebnis (kein Scan).
	case typeBounded:
		// Kostenbasierte Index-Wahl (ADR-023): den selektiveren von Typ- und
		// Subject-Index wählen. Beide Pfade liefern dasselbe Ergebnis; nur die
		// Kosten (Anzahl angefasster Events) unterscheiden sich.
		typeCost, errT := s.store.CountByTypes(reqTypes)
		subjCost, errS := s.store.CountSubject(req.Subject, req.Recursive)
		if errT == nil && errS == nil && subjCost < typeCost {
			// Subject-Index günstiger: Teilbaum scannen, Typ-Filter einschieben.
			optsT := opts
			optsT.Types = reqTypes
			var events []event.Event
			events, scanErr = s.store.Read(req.Subject, req.Recursive, optsT)
			for _, ev := range events {
				if !collect(ev) {
					break
				}
			}
		} else {
			// Typ-Index günstiger (oder Kostenschätzung fehlgeschlagen → sicherer
			// Default): nur die geforderten Typen laden, Subject nachfiltern.
			scanErr = s.store.ReadByTypesFunc(reqTypes, opts, func(ev event.Event) bool {
				if !store.MatchSubject(ev.Subject, req.Subject, req.Recursive) {
					return true
				}
				return collect(ev)
			})
		}
	default:
		// Kein sicherer Typ-Filter → vollständiger Scan des Scopes.
		var events []event.Event
		events, scanErr = s.store.Read(req.Subject, req.Recursive, opts)
		for _, ev := range events {
			if !collect(ev) {
				break
			}
		}
	}
	if scanErr != nil {
		s.logger.Error("run-query fehlgeschlagen", "err", scanErr)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}

	if len(req.Select) == 0 {
		writeNDJSON(w, s.logger, result)
		return
	}

	// Projektion: jedes Treffer-Event auf die gewählten Feldpfade reduzieren.
	projected := make([]map[string]any, 0, len(result))
	for _, ev := range result {
		obj, err := query.Project(ev, req.Select)
		if err != nil {
			s.logger.Error("run-query projektion fehlgeschlagen", "err", err)
			writeError(w, http.StatusInternalServerError, "interner fehler bei der projektion")
			return
		}
		projected = append(projected, obj)
	}
	writeNDJSON(w, s.logger, projected)
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

// problemContentType ist der Media-Type für RFC-7807-Fehler.
const problemContentType = "application/problem+json"

// problemDetails ist ein strukturierter Fehler-Body nach RFC 7807
// (application/problem+json). `type` bleibt generisch ("about:blank"); `title`
// ist der HTTP-Statustext, `detail` die konkrete Meldung.
type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// writeError schreibt einen Fehler als application/problem+json (RFC 7807) —
// ein konfliktfreier Quick Win Richtung Swiss API Guidelines (ADR-019). Die
// Signatur bleibt unverändert, damit alle Aufrufstellen profitieren.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetails{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}

// writeNDJSON schreibt eine Werteliste als Newline-Delimited JSON (ein JSON-
// Objekt pro Zeile). Generisch, damit sowohl Events als auch projizierte
// Objekte ausgegeben werden können.
func writeNDJSON[T any](w http.ResponseWriter, logger *slog.Logger, items []T) {
	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, ev := range items {
		if err := enc.Encode(ev); err != nil {
			// Header sind bereits gesendet; nur noch loggen.
			logger.Error("ndjson schreiben fehlgeschlagen", "err", err)
			return
		}
	}
}
