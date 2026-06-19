package httpapi

import (
	"net/http"
	"strconv"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/store"
)

// Audit-Log (ADR-025): jede Autorisierungsentscheidung der scope-bewussten
// Middleware wird strukturiert protokolliert — „wer / wann / welche Route /
// Ergebnis". Erfolgreiche wie abgelehnte Zugriffe sind sicherheitsrelevant und
// werden beide geloggt. Es landet NIE ein Geheimnis (secret/hash) im Log; nur
// der nicht-geheime kid und der Name.
//
// Hinweis zur `status`-Semantik: das ist der Status der Autorisierung (200 =
// zugelassen, 401/403 = abgelehnt). Der endgültige HTTP-Status der Antwort wird
// zusätzlich von der Observability-Middleware (instrument) je Request geloggt.

// auditDecision protokolliert eine Autorisierungsentscheidung. kid/name werden
// nur geloggt, wenn bekannt (bei 401 ohne gültigen Header fehlt der kid).
func (s *Server) auditDecision(r *http.Request, scope auth.Scope, decision string, status int, kid, name string) {
	attrs := []any{
		"audit", true,
		"method", r.Method,
		"path", r.URL.Path,
		"scope", string(scope),
		"decision", decision,
		"status", status,
	}
	if kid != "" {
		attrs = append(attrs, "kid", kid)
	}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	s.logger.Info("audit", attrs...)
}

// recordAudit schreibt einen administrativen Audit-Eintrag (ADR-031) in den
// persistenten audit_log-Bucket. Der Actor stammt aus der authentifizierten
// Identität des Requests (leer bei system). Ein leeres errMsg bedeutet Erfolg;
// ein gesetztes Ergebnis markiert den Eintrag als Misserfolg. Best effort: ein
// Schreibfehler wird nur geloggt und bricht die eigentliche Aktion nicht ab —
// das Audit darf den Betrieb nicht blockieren.
func (s *Server) recordAudit(r *http.Request, action, target, errMsg string) {
	e := store.AuditEntry{Action: action, Target: target}
	if errMsg == "" {
		e.Result = store.AuditSuccess
	} else {
		e.Result = store.AuditFailure
		e.Error = errMsg
	}
	if id, ok := identityFromContext(r); ok {
		e.ActorKID = id.KID
		e.ActorName = id.Name
	}
	if err := s.store.AppendAudit(e); err != nil {
		s.logger.Error("audit-eintrag schreiben fehlgeschlagen", "action", action, "err", err)
	}
}

// handleAudit liefert die jüngsten administrativen Audit-Einträge (ADR-031),
// read-only. Scope `audit` oder `admin` (requireAnyScope). Query-Parameter:
// limit (Default 100, max 1000) und before (Cursor: nur Seq < before).
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	var before uint64
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			before = n
		}
	}

	entries, err := s.store.AuditEntries(limit, before)
	if err != nil {
		s.logger.Error("audit lesen fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	total, err := s.store.CountAudit()
	if err != nil {
		s.logger.Error("audit zählen fehlgeschlagen", "err", err)
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
}
