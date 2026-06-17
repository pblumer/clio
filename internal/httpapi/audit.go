package httpapi

import (
	"net/http"

	"github.com/pblumer/clio/internal/auth"
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
