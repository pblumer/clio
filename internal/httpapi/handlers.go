package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/clio/internal/apidocs"
	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/eventstats"
	"github.com/pblumer/clio/internal/metrics"
	"github.com/pblumer/clio/internal/query"
	"github.com/pblumer/clio/internal/store"
)

// observeHeartbeat ist das Intervall, in dem ein offener observe-Stream eine
// Leerzeile sendet. Das hält die Verbindung gegen Idle-Timeouts offen und
// zwingt puffernde Reverse-Proxies (Firmennetze), Daten durchzureichen, statt
// die nie endende Antwort zurückzuhalten. Der Client ignoriert Leerzeilen.
// Variable (nicht const), damit Tests das Intervall verkürzen können.
var observeHeartbeat = 15 * time.Second

// queryHeartbeat ist das Intervall, in dem ein laufender run-query-Scan eine
// Leerzeile sendet, solange noch kein Treffer geflossen ist. Ein selektives
// Prädikat über einen breiten Scope kann lange scannen, bevor (wenn überhaupt)
// der erste Treffer kommt; ohne ein Lebenszeichen erreichen weder Header noch
// Body den Reverse-Proxy, der die Upstream-Verbindung dann nach seinem
// Read-Timeout zurücksetzt (502 am Ingress). Die Leerzeile hält die Verbindung
// offen und stupst puffernde Proxies an. Der Client ignoriert Leerzeilen (wie
// beim observe-Stream). Variable (nicht const), damit Tests sie verkürzen können.
var queryHeartbeat = 15 * time.Second

// defaultReadLimit ist die Obergrenze an Events, die read-events / GET-events
// ohne explizites `limit` zurückgeben. Sie schützt vor versehentlich breiten
// Reads (z. B. „/" über Millionen Events), die einen Client/das Dashboard
// überschwemmen. Da Reads streamen (konstanter Server-Speicher), ist ein
// größeres explizites Limit unbedenklich; 0 im Request bedeutet „Default".
const defaultReadLimit = 10_000

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
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
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

	body := map[string]any{
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
	}

	// Speicherbelegung der DB-Datei inkl. Füllgrad (Datei vs. wiederverwendbarer
	// freier Platz). Informativ — schlägt das fehl, bleibt /info trotzdem nutzbar.
	if st, err := s.store.Stats(); err != nil {
		s.logger.Error("info: db-statistik fehlgeschlagen", "err", err)
	} else {
		body["databaseFileBytes"] = st.FileBytes
		body["databaseDataBytes"] = st.DataBytes
		body["databaseUsedBytes"] = st.UsedBytes
		body["databaseFreeBytes"] = st.FreeBytes
		body["databaseFillPercent"] = math.Round(st.FillPercent*10) / 10
		// Vorbelegte Grenze (CLIO_DB_INITIAL_MB) und wie weit der genutzte Umfang
		// daran heranreicht — der Remap-Headroom. Nur wenn vorbelegt.
		if initial := int64(s.cfg.DBInitialMB) << 20; initial > 0 {
			body["databaseInitialBytes"] = initial
			body["databaseInitialFillPercent"] = math.Round(float64(st.DataBytes)/float64(initial)*1000) / 10
		}
	}

	// Storage-Betriebsstatus (für Dashboard/Betrieb): Auto-Compaction- und
	// Monitor-Konfiguration sowie der letzte Online-Compact dieser Laufzeit.
	body["dbCompactEnabled"] = s.cfg.DBCompactEnabled
	if s.cfg.DBCompactEnabled {
		body["dbCompactIntervalH"] = s.cfg.DBCompactIntervalH
	}
	body["dbGrowThresholdPct"] = s.cfg.DBGrowThresholdPct
	if lc, ok := s.store.LastCompaction(); ok {
		body["databaseLastCompaction"] = map[string]any{
			"at":       lc.At.Format(time.RFC3339Nano),
			"oldBytes": lc.OldBytes,
			"newBytes": lc.NewBytes,
		}
	}

	writeJSON(w, http.StatusOK, body)
}

// recordEventStats schreibt die geschriebenen Events ins Eventstrom-Histogramm
// fort — nach Server-Zeit, aufgeschlüsselt nach `source`. Pro Source wird nur
// einmal gebucht (gebündelt), um Lock-Wechsel gering zu halten.
func (s *Server) recordEventStats(written []event.Event) {
	if len(written) == 0 {
		return
	}
	now := time.Now().UTC()
	bySource := make(map[string]int, 4)
	for _, ev := range written {
		bySource[ev.Source]++
	}
	for source, n := range bySource {
		s.events.AddSource(n, now, source)
	}
}

// handleEventStats liefert das Histogramm der Events über die Zeit (nach
// Event-Zeit; beim Start aus der Historie aufgebaut): Startzeitpunkt,
// Bucket-Breite (Sekunden) und die Bucket-Zähler. So kann das /ui-Dashboard die
// Eventmengen über die Zeitachse zeichnen, ohne die gesamte Historie zu streamen.
func (s *Server) handleEventStats(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
	if r.URL.Query().Get("by") == "source" {
		snap := s.events.SnapshotBySource()
		// Sentinel-Schlüssel der Overflow-Serie auf ein lesbares Label abbilden.
		sources := make(map[string][]uint64, len(snap.Sources))
		for k, v := range snap.Sources {
			if k == eventstats.OverflowSource {
				k = "andere"
			}
			sources[k] = v
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"start":         snap.Origin.Format(time.RFC3339Nano),
			"bucketSeconds": snap.Width.Seconds(),
			"counts":        snap.Counts,
			"sources":       sources,
			"total":         snap.Total,
			"serverTime":    time.Now().UTC().Format(time.RFC3339Nano),
		})
		return
	}

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
	q := r.URL.Query()
	// `children` lädt nur die direkten Kinder eines Knotens seitenweise — der
	// skalierbare Pfad für den Explorer-Baum bei sehr vielen Subjects (statt den
	// kompletten Baum mit `tree=true` zu materialisieren).
	if _, ok := q["children"]; ok {
		s.handleSubjectChildren(w, r)
		return
	}
	prefix := q.Get("prefix")
	if prefix != "" && prefix[0] != '/' {
		writeError(w, http.StatusBadRequest, "prefix muss mit \"/\" beginnen")
		return
	}
	// Subject-Berechtigung (ADR-033): Listing über den Prefix-Teilbaum (rekursiv);
	// ohne Prefix = ganzer Baum, verlangt globalen read.
	base := prefix
	if base == "" {
		base = "/"
	}
	if !s.authorizeSubject(w, r, auth.ScopeRead, base, true) {
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

// defaultChildrenLimit / maxChildrenLimit begrenzen, wie viele direkte Kinder
// ein einzelner read-subjects?children=…-Aufruf liefert. Die Obergrenze schützt
// den Server (und den Browser) davor, bei breiten Knoten — z. B. Millionen
// Subjects unter einem Pfad — alles auf einmal zu materialisieren.
const (
	defaultChildrenLimit = 500
	maxChildrenLimit     = 5000
)

// subjectChildrenResponse ist die Antwort von read-subjects?children=…: die
// direkten Kinder eines Knotens als eine Seite. nextAfter ist gesetzt, wenn
// weitere Kinder folgen (als `after` der nächsten Anfrage übergeben). count/
// total beschreiben den Eltern-Knoten und werden nur auf der ersten Seite
// (ohne `after`) berechnet, um wiederholte Teilbaum-Scans zu vermeiden.
type subjectChildrenResponse struct {
	Parent    string            `json:"parent"`
	Count     uint64            `json:"count"`
	Total     uint64            `json:"total"`
	Children  []store.ChildInfo `json:"children"`
	NextAfter string            `json:"nextAfter,omitempty"`
}

// handleSubjectChildren liefert die direkten Kinder eines Knotens im Subject-
// Baum seitenweise (?children=<pfad>&after=<cursor>&limit=<n>). So lädt der
// Explorer den Baum schrittweise statt den gesamten Baum auf einmal.
func (s *Server) handleSubjectChildren(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	parent := q.Get("children")
	if parent == "" {
		parent = "/"
	}
	if parent[0] != '/' {
		writeError(w, http.StatusBadRequest, "children muss mit \"/\" beginnen")
		return
	}
	if !s.authorizeSubject(w, r, auth.ScopeRead, parent, true) {
		return
	}
	limit := defaultChildrenLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit muss eine positive ganze Zahl sein")
			return
		}
		if n > maxChildrenLimit {
			n = maxChildrenLimit
		}
		limit = n
	}
	after := q.Get("after")

	children, more, err := s.store.Children(parent, after, limit)
	if err != nil {
		s.logger.Error("read-subjects children fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	resp := subjectChildrenResponse{Parent: parent, Children: children}
	if more && len(children) > 0 {
		resp.NextAfter = children[len(children)-1].Subject
	}
	// Eltern-Zähler nur auf der ersten Seite: total = Teilbaum-Summe, count =
	// Events exakt auf dem Eltern-Subject. Der rekursive Scan ist teurer und
	// auf Folgeseiten unnötig (der Knoten ist bereits beschriftet).
	if after == "" {
		if total, err := s.store.CountSubject(parent, true); err == nil {
			resp.Total = total
		}
		if count, err := s.store.DirectCount(parent); err == nil {
			resp.Count = count
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
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
	// Schemas gelten typweit (subjektübergreifend) → globaler write (ADR-033).
	if !s.authorizeGlobal(w, r, auth.ScopeWrite) {
		return
	}
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
		s.recordAudit(r, store.AuditActionSchemaRegister, req.Type, "")
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
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
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
	size, data, used, free := int64(-1), int64(-1), int64(-1), int64(-1)
	if st, err := s.store.Stats(); err != nil {
		s.logger.Error("db-größe ermitteln fehlgeschlagen", "err", err)
	} else {
		size, data, used, free = st.FileBytes, st.DataBytes, st.UsedBytes, st.FreeBytes
	}
	initial := int64(s.cfg.DBInitialMB) << 20
	diskFree, diskTotal, err := s.store.DiskUsage()
	if err != nil {
		s.logger.Error("disk-usage ermitteln fehlgeschlagen", "err", err)
		diskFree = -1
		diskTotal = -1
	}
	online := 0
	for _, sn := range s.activity.Snapshot(time.Now().UTC()) {
		if sn.Online {
			online++
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.Write(w, metrics.Gauges{
		ActiveObservers: s.broker.SubscriberCount(),
		OnlineKeys:      online,
		EventsTotal:     count,
		DBSizeBytes:     size,
		DBDataBytes:     data,
		DBInitialBytes:  initial,
		DBUsedBytes:     used,
		DBFreeBytes:     free,
		DiskFreeBytes:   diskFree,
		DiskTotalBytes:  diskTotal,
	})
}

// handleBackup streamt einen konsistenten Online-Snapshot der gesamten Datenbank
// als bbolt-Datei (application/octet-stream, ADR-026). Admin-scoped: das Artefakt
// enthält die gesamte Historie samt Schlüsselbund (nur Secret-Hashes, nie
// Klartext). Der Snapshot läuft in einer Read-Transaktion und blockiert keine
// Schreiber (echtes Hot-Backup). Die Schreib-Deadline wird wie bei den großen
// Lese-Routen aufgehoben, sonst kappt WriteTimeout große Backups.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	filename := "clio-" + time.Now().UTC().Format("20060102T150405Z") + ".clio"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	// Sobald Backup zu schreiben beginnt, ist der 200-Header raus — ein Fehler
	// mitten im Stream lässt sich dann nur noch loggen (der Client erkennt das
	// abgeschnittene Artefakt an `verify`).
	res, err := s.store.Backup(w)
	if err != nil {
		s.logger.Error("backup streamen fehlgeschlagen", "err", err)
		s.recordAudit(r, store.AuditActionBackup, "", err.Error())
		return
	}
	if id, ok := identityFromContext(r); ok {
		s.logger.Info("backup gestreamt", "by", id.KID, "events", res.Events, "bytes", res.Bytes, "head", res.Head)
	}
	s.recordAudit(r, store.AuditActionBackup, fmt.Sprintf("events=%d,bytes=%d", res.Events, res.Bytes), "")
}

// handleVerify rechnet die Hash-Kette nach und meldet, ob die Historie
// unverändert ist. Eine erkannte Manipulation ergibt HTTP 200 mit ok=false
// (die Prüfung selbst war erfolgreich) — erst ein interner Fehler ergibt 500.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
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
		// Der Subject-Raum /_clio/ ist server-only (ADR-030): Clients dürfen dort
		// nicht schreiben, sonst ließen sich z. B. Login-Events fälschen. Bewusst
		// 403 (verboten), nicht 400 — die Anfrage ist wohlgeformt, nur unzulässig.
		if isReservedSubject(c.Subject) {
			writeError(w, http.StatusForbidden, "events["+strconv.Itoa(i)+"]: subject-präfix "+reservedSubjectPrefix+" ist reserviert (server-only)")
			return
		}
		// Subject-Berechtigung (ADR-033): JEDES Event muss in einem write-Grant
		// liegen, sonst 403 — atomar, es wurde noch nichts geschrieben.
		if !s.authorizeSubject(w, r, auth.ScopeWrite, c.Subject, false) {
			return
		}
	}

	preconditions, err := s.parsePreconditions(req.Preconditions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	written, err := s.store.AppendAuthored(req.Events, preconditions, s.authorKID(r))
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
	s.recordEventStats(written)

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
	Limit      int      `json:"limit"` // 0 = Default-Obergrenze (defaultReadLimit)
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
	if !s.authorizeSubject(w, r, auth.ScopeRead, req.Subject, req.Recursive) {
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
	limit, err := effectiveReadLimit(req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.doRead(w, req.Subject, req.Recursive, store.ReadOptions{
		LowerBound: lower,
		UpperBound: upper,
		Types:      req.Types,
	}, limit)
}

// effectiveReadLimit validiert ein Roh-Limit und setzt den Default ein. 0 → Default,
// negativ → Fehler.
func effectiveReadLimit(raw int) (int, error) {
	if raw < 0 {
		return 0, errors.New("limit darf nicht negativ sein")
	}
	if raw == 0 {
		return defaultReadLimit, nil
	}
	return raw, nil
}

// doRead liest Events und streamt sie als NDJSON. Gemeinsamer Kern von
// read-events (POST) und der GET-Pfad-Route. limit > 0 begrenzt die Ausgabe und
// bricht den Scan vorzeitig ab.
//
// Anders als früher wird NICHT erst das gesamte Ergebnis im Speicher
// materialisiert: über store.ReadFunc wird Event für Event kodiert und periodisch
// geflusht — der Server-Speicher bleibt konstant, unabhängig von der Trefferzahl.
// Tradeoff des Streamings: der 200-Header geht raus, bevor ein etwaiger
// Lesefehler bekannt ist; ein (seltener) Fehler mitten im Stream wird daher nur
// geloggt, der Status bleibt 200 (wie beim observe-Stream).
func (s *Server) doRead(w http.ResponseWriter, subject string, recursive bool, opts store.ReadOptions, limit int) {
	w.Header().Set("Content-Type", ndjsonContentType)
	if limit > 0 {
		w.Header().Set("X-Clio-Result-Limit", strconv.Itoa(limit))
	}
	w.WriteHeader(http.StatusOK)

	// Streaming-Antwort variabler Dauer (datenabhängig, durch limit begrenzt) — die
	// pauschale Server-WriteTimeout passt hier nicht; Deadline für diese Verbindung
	// aufheben (Fehler ignorieren, s. doObserve). Die gepufferten Handler behalten
	// den 30s-Schutz.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	var n int
	var encErr error
	readErr := s.store.ReadFunc(subject, recursive, opts, func(ev event.Event) bool {
		if encErr = enc.Encode(ev); encErr != nil {
			return false // Schreiben fehlgeschlagen (Client weg) → abbrechen
		}
		n++
		if flusher != nil && n%512 == 0 {
			flusher.Flush()
		}
		return limit == 0 || n < limit
	})
	if flusher != nil {
		flusher.Flush()
	}
	switch {
	case readErr != nil:
		// Header sind bereits gesendet; nur noch loggen.
		s.logger.Error("read fehlgeschlagen (stream bereits begonnen)", "err", readErr)
	case encErr != nil:
		s.logger.Error("ndjson schreiben fehlgeschlagen", "err", encErr)
	}
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
	if !s.authorizeSubject(w, r, auth.ScopeRead, subject, recursive) {
		return
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

	rawLimit := 0
	if v := q.Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit muss eine ganze Zahl sein")
			return
		}
		rawLimit = parsed
	}
	limit, err := effectiveReadLimit(rawLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.doRead(w, subject, recursive, store.ReadOptions{LowerBound: lower, UpperBound: upper, Types: types}, limit)
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
	if !s.authorizeSubject(w, r, auth.ScopeRead, req.Subject, req.Recursive) {
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

	// observe ist ein potenziell unendlicher Stream — die Server-WriteTimeout darf
	// ihn nicht kappen. Schreib-Deadline für diese Verbindung aufheben (Fehler
	// ignorieren: nicht jeder ResponseWriter unterstützt das, z. B. im Test).
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	// Offene Observe-Verbindung als Presence verbuchen (ADR-030): solange die
	// Verbindung steht, gilt der Schlüssel als online. Die Identität setzt
	// requireScope in den Context (read-Scope). Ohne Identität (sollte für die
	// geschützten Routen nicht vorkommen) wird nichts verbucht.
	if id, ok := identityFromContext(r); ok {
		s.activity.OpenObserve(id.KID, id.Name, scopeStrings(id.Scopes), time.Now().UTC())
		defer s.activity.CloseObserve(id.KID, time.Now().UTC())
	}

	// Zuerst abonnieren, dann History lesen: so geht kein Event verloren, das
	// zwischen History-Snapshot und Live-Phase geschrieben wird. Doppelte
	// werden über die ID (lastID) verworfen.
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	typeFilter := typeSet(types)

	w.Header().Set("Content-Type", ndjsonContentType)
	// Reverse-Proxies (z. B. nginx) nicht puffern lassen — sonst hält der Proxy
	// den nie endenden Stream zurück und der Client sieht nie Header/Bytes.
	w.Header().Set("X-Accel-Buffering", "no")
	// no-transform verbietet Zwischen-Proxies, die Antwort umzukodieren/zu
	// komprimieren — Komprimieren erzwingt Pufferung und hält den Stream zurück.
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)

	// Sofort flushen, damit der Client die offene Verbindung umgehend sieht — auch
	// ohne History und ohne neue Events. Ohne diesen Anstoß hält ein puffernder
	// Reverse-Proxy die reine Header-Antwort zurück, bis das erste Body-Byte kommt.
	// Zusätzlich ein optionales, ausreichend großes Whitespace-Polster
	// (ObservePreambleBytes): manche Security-Gateways geben einen gepufferten
	// Stream erst weiter, wenn genug Bytes geflossen sind — das Polster kippt sie in
	// den Streaming-Modus. Whitespace-/Blankzeilen sind im NDJSON-Stream Protokoll
	// (Heartbeat) und werden klientseitig ignoriert.
	if n := s.cfg.ObservePreambleBytes; n > 0 {
		if _, err := w.Write(bytes.Repeat([]byte(" "), n)); err != nil {
			return
		}
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return
	}
	flusher.Flush()

	// lastID = höchste bereits ausgelieferte ID. Initial untere Grenze − 1,
	// damit Live-Events ab lowerBound und nur neuer als die History kommen.
	var lastID uint64
	if lower > 0 {
		lastID = lower - 1
	}
	// History streamend ausliefern (nicht erst komplett in den Speicher laden) —
	// ein Reconnect mit niedrigem lowerBound über einen breiten Scope bliebe sonst
	// ein OOM-Pfad. Live-Events, die währenddessen eintreffen, puffert der Broker
	// im Subscriber-Channel; die ID-Deduplizierung (lastID) verwirft Dubletten.
	var encErr error
	histErr := s.store.ReadFunc(subject, recursive, store.ReadOptions{LowerBound: lower, Types: types}, func(ev event.Event) bool {
		if encErr = enc.Encode(ev); encErr != nil {
			return false
		}
		if id, perr := strconv.ParseUint(ev.ID, 10, 64); perr == nil && id > lastID {
			lastID = id
		}
		return true
	})
	if encErr != nil {
		return // Client weg
	}
	if histErr != nil {
		// Header/200 sind bereits gesendet; nur loggen und Verbindung schließen.
		s.logger.Error("observe history fehlgeschlagen (stream bereits begonnen)", "err", histErr)
		return
	}
	flusher.Flush()

	// Heartbeat: hält die Verbindung offen und stupst puffernde Proxies an.
	beat := time.NewTicker(observeHeartbeat)
	defer beat.Stop()

	// encodeIfMatch kodiert ev in den Stream, sofern es im Scope/Typ-Filter liegt
	// und neuer als lastID ist — ohne zu flushen. Liefert false nur bei einem
	// Schreibfehler (Client weg); der Aufrufer beendet dann den Stream.
	encodeIfMatch := func(ev event.Event) bool {
		id, perr := strconv.ParseUint(ev.ID, 10, 64)
		if perr != nil || id <= lastID {
			return true
		}
		if !store.MatchSubject(ev.Subject, subject, recursive) {
			return true
		}
		if typeFilter != nil {
			if _, ok := typeFilter[ev.Type]; !ok {
				return true
			}
		}
		if err := enc.Encode(ev); err != nil {
			return false
		}
		lastID = id
		return true
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Lost:
			return
		case <-beat.C:
			if _, err := w.Write([]byte("\n")); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-sub.Events:
			if !encodeIfMatch(ev) {
				return
			}
			// Burst zusammenfassen: alle bereits gepufferten Events kodieren und
			// erst danach EINMAL flushen. Ein Flush pro Event hält bei hoher
			// Event-Rate (~1000/s) nicht Schritt — der Subscriber-Puffer liefe über
			// und der Subscriber würde abgehängt (sichtbar als Reconnect-Flattern).
			for drained := false; !drained; {
				select {
				case ev2 := <-sub.Events:
					if !encodeIfMatch(ev2) {
						return
					}
				default:
					drained = true
				}
			}
			flusher.Flush()
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
	Limit      int      `json:"limit"`  // 0 = Default-Obergrenze (defaultReadLimit)
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
	if !s.authorizeSubject(w, r, auth.ScopeRead, req.Subject, req.Recursive) {
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

	// limit==0 → Default-Obergrenze (wie read-events). Schützt vor breiten Queries
	// mit vielen Treffern. req.Limit<0 ist oben bereits als 400 abgefangen.
	limit, err := effectiveReadLimit(req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Typ-Constraint aus dem Prädikat ableiten: Schränkt es den event.type
	// zwingend ein, laden wir nur die Events dieser Typen über den Typ-Index —
	// statt den ganzen Scope zu scannen (ADR-021). Vor dem Senden der Header.
	var reqTypes []string
	typeBounded := false
	if pred != nil {
		reqTypes, typeBounded = pred.RequiredTypes()
	}

	// Eine Query ohne Typ-Constraint kann den Typ-Index nicht nutzen und scannt
	// den gesamten Scope; referenziert das Prädikat zusätzlich event.data, wird pro
	// Event der Payload deserialisiert (teuerster Pfad — genau der gemeldete 502-
	// Fall, ADR-028). Das melden wir als Warn-Header, damit Clients/das Dashboard auf den
	// fehlenden Index hinweisen und einen engeren Scope bzw. ein `event.type ==`
	// empfehlen können. Nur ein Hinweis — die Query läuft weiterhin (Kompatibilität).
	if pred != nil && !typeBounded {
		warn := "prädikat nutzt keinen typ-index (event.type-constraint fehlt) → vollständiger scan über den scope"
		if pred.UsesData() {
			warn += "; data-zugriff deserialisiert jedes event"
		}
		w.Header().Set("X-Clio-Query-Warning", warn)
	}

	// Query-Deadline (ADR-028): begrenzt die Scan-Dauer und damit die Haltezeit der
	// bbolt-Lesetransaktion (ein langer Read unter Schreiblast blockiert die Wieder-
	// verwendung freier Seiten → DB-/Speicherwachstum). 0 = aus (rückwärts-
	// kompatibel). Der Scan-Loop prüft ctx in `guard` und bricht sauber ab.
	ctx := r.Context()
	if s.cfg.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.QueryTimeout)
		defer cancel()
	}

	// Ab hier wird gestreamt: Header senden, Schreib-Deadline aufheben (ein Scan
	// über einen breiten Scope mit selektivem Prädikat kann lange dauern und darf
	// nicht von der Server-WriteTimeout gekappt werden), dann jeden Treffer direkt
	// herausschreiben — die Treffermenge wird NICHT mehr komplett im Speicher
	// gesammelt (konstanter Speicher, auch bei Millionen Events). Das Limit
	// begrenzt zusätzlich die Ausgabe und bricht den Scan früh ab.
	w.Header().Set("Content-Type", ndjsonContentType)
	// Reverse-Proxies nicht puffern lassen — sonst hält der Proxy Header und
	// Heartbeats zurück, bis genug Body geflossen ist (wie beim observe-Stream).
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	if limit > 0 {
		w.Header().Set("X-Clio-Result-Limit", strconv.Itoa(limit))
	}
	w.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)

	// Sofort ein erstes Lebenszeichen flushen, damit die Header umgehend den Proxy
	// erreichen (Time-to-first-byte ≈ 0 statt = Scan-Dauer). Ohne diesen Anstoß hält
	// ein puffernder Reverse-Proxy die reine Header-Antwort zurück, bis das erste
	// Body-Byte kommt — bei einem selektiven Prädikat erst am Scan-Ende. Leerzeile =
	// Heartbeat, vom Client ignoriert (wie beim observe-Stream).
	if flusher != nil {
		_, _ = w.Write([]byte("\n"))
		flusher.Flush()
	}

	var (
		n         int
		emitErr   error
		aborted   bool // Scan per Deadline/Client-Abbruch beendet
		lastWrite = time.Now()
	)

	// guard läuft vor jedem gescannten Event: prüft die Deadline/den Client-Abbruch
	// und sendet bei Bedarf einen Heartbeat, solange noch kein Treffer geflossen ist.
	// Rückgabe false bricht den Scan sauber ab.
	guard := func() bool {
		if ctx.Err() != nil {
			aborted = true
			return false
		}
		if flusher != nil && time.Since(lastWrite) >= queryHeartbeat {
			if _, err := w.Write([]byte("\n")); err != nil {
				emitErr = err
				return false
			}
			flusher.Flush()
			lastWrite = time.Now()
		}
		return true
	}

	// emit wendet das Prädikat an und schreibt jeden Treffer (ggf. projiziert)
	// direkt heraus. Rückgabe true = weiter scannen, false = genug/Abbruch.
	emit := func(ev event.Event) bool {
		if pred != nil {
			ok, perr := pred.Eval(ev)
			if perr != nil || !ok {
				return true
			}
		}
		var out any = ev
		if len(req.Select) > 0 {
			obj, perr := query.Project(ev, req.Select)
			if perr != nil {
				emitErr = perr
				return false
			}
			out = obj
		}
		if emitErr = enc.Encode(out); emitErr != nil {
			return false
		}
		n++
		lastWrite = time.Now()
		if flusher != nil && n%512 == 0 {
			flusher.Flush()
		}
		return limit == 0 || n < limit
	}

	// scan koppelt den Heartbeat-/Deadline-guard vor jeden Treffer-Versuch. Der
	// Typ-Index-Pfad ruft guard selbst auf (auch für übersprungene Subjects).
	scan := func(ev event.Event) bool {
		if !guard() {
			return false
		}
		return emit(ev)
	}

	// Daten-Index-Wahl (ADR-029): Verlangt das Prädikat genau einen Typ und eine
	// `event.data.<feld> == '<wert>'`-Gleichheit auf einem für diesen Typ
	// indizierten Feld, beantwortet ein direkter Wert-Lookup die Query — statt
	// alle Events des Typs zu laden und jede Payload zu deserialisieren (der teure
	// Pfad aus ADR-028). Die Gleichheit ist eine notwendige Bedingung, der Lookup
	// verliert also keine Treffer; Subject und Restprädikat prüft `scan`/`emit`.
	var diType, diField, diValue string
	useDataIdx := false
	if pred != nil && len(reqTypes) == 1 {
		for _, eq := range pred.DataEqualities() {
			if s.store.DataFieldIndexed(reqTypes[0], eq.Field) {
				diType, diField, diValue = reqTypes[0], eq.Field, eq.Value
				useDataIdx = true
				break
			}
		}
	}

	var scanErr error
	switch {
	case typeBounded && len(reqTypes) == 0:
		// Kein Typ kann das Prädikat erfüllen → leeres Ergebnis (kein Scan).
	case useDataIdx:
		// Direkter Wert-Lookup über den Daten-Index; Subject-Scope nachfiltern.
		scanErr = s.store.ReadByDataFieldFunc(diType, diField, diValue, opts, func(ev event.Event) bool {
			if !guard() {
				return false
			}
			if !store.MatchSubject(ev.Subject, req.Subject, req.Recursive) {
				return true
			}
			return emit(ev)
		})
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
			scanErr = s.store.ReadFunc(req.Subject, req.Recursive, optsT, scan)
		} else {
			// Typ-Index günstiger (oder Kostenschätzung fehlgeschlagen → sicherer
			// Default): nur die geforderten Typen laden, Subject nachfiltern.
			scanErr = s.store.ReadByTypesFunc(reqTypes, opts, func(ev event.Event) bool {
				if !guard() {
					return false
				}
				if !store.MatchSubject(ev.Subject, req.Subject, req.Recursive) {
					return true
				}
				return emit(ev)
			})
		}
	default:
		// Kein sicherer Typ-Filter → vollständiger Scan des Scopes (streamend,
		// bricht bei erreichtem Limit ab — kein Materialisieren des ganzen Scopes).
		scanErr = s.store.ReadFunc(req.Subject, req.Recursive, opts, scan)
	}

	if flusher != nil {
		flusher.Flush()
	}
	// Header/200 sind bereits gesendet — etwaige Fehler nur noch loggen. Ein
	// Deadline-/Abbruch-Treffer beendet den Stream bewusst (der Client erhält ein
	// definiertes, ggf. unvollständiges Ergebnis statt einer hängenden Verbindung);
	// der Status bleibt 200, weil bereits gestreamt wurde.
	switch {
	case aborted:
		s.logger.Warn("run-query abgebrochen (deadline/client, stream bereits begonnen)",
			"err", ctx.Err(), "subject", req.Subject, "treffer", n)
	case emitErr != nil:
		s.logger.Error("run-query ausgabe fehlgeschlagen (stream bereits begonnen)", "err", emitErr)
	case scanErr != nil:
		s.logger.Error("run-query fehlgeschlagen (stream bereits begonnen)", "err", scanErr)
	}
}
