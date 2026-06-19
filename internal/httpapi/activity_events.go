package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/clio/internal/activity"
	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
)

// Aktivität & Presence (ADR-030): Sichten auf „wer ist online / wer tut was"
// sowie die optionalen Auth-Lifecycle-Events (Dogfooding). Diese Datei bündelt
// den Lese-Endpunkt, den reservierten Subject-Namespace und den Emitter, der —
// nur bei aktiviertem CLIO_AUTH_EVENTS — Lifecycle-Ereignisse als echte
// CloudEvents in den Event-Strom schreibt.

const (
	// reservedSubjectPrefix kennzeichnet den server-only Subject-Raum. Clients
	// dürfen hier NICHT schreiben (sonst ließen sich z. B. Login-Events fälschen);
	// nur der Server selbst legt darunter Events ab.
	reservedSubjectPrefix = "/_clio/"

	// authEventSource ist die feste, serverkontrollierte Herkunft der
	// Lifecycle-Events (nicht client-setzbar).
	authEventSource = "clio://auth"

	subjectAuthSessions = "/_clio/auth/sessions/"
	subjectAuthKeys     = "/_clio/auth/keys/"
	subjectAuthDenied   = "/_clio/auth/denied/"

	typeSessionStarted = "dev.clio.auth.session-started"
	typeSessionEnded   = "dev.clio.auth.session-ended"
	typeKeyCreated     = "dev.clio.auth.key-created"
	typeKeyRevoked     = "dev.clio.auth.key-revoked"
	typeAccessDenied   = "dev.clio.auth.access-denied"
)

// isReservedSubject meldet, ob subject im server-only Namespace /_clio/ liegt.
func isReservedSubject(subject string) bool {
	return subject == strings.TrimSuffix(reservedSubjectPrefix, "/") ||
		strings.HasPrefix(subject, reservedSubjectPrefix)
}

// handleActivity liefert den Presence-/Aktivitäts-Snapshot (Scope admin,
// ADR-030): wer ist online, wer hat wann zuletzt gelesen/geschrieben. Reiner
// Laufzeit-State (in-memory), enthält keine Geheimnisse.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	snaps := s.activity.Snapshot(now)
	online := 0
	for _, sn := range snaps {
		if sn.Online {
			online++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"serverTime":            now.Format(time.RFC3339Nano),
		"presenceWindowSeconds": s.cfg.PresenceWindow.Seconds(),
		"authEvents":            s.cfg.AuthEvents,
		"onlineCount":           online,
		"keys":                  snaps,
	})
}

// writeInternalEvent schreibt ein server-erzeugtes CloudEvent in den Event-Strom
// — direkt über den Store (kein HTTP-Auth-Pfad, daher keine Rekursion und kein
// Henne-Ei-Problem). No-op, solange CLIO_AUTH_EVENTS aus ist. Schreibfehler
// werden geloggt, brechen aber den auslösenden Vorgang NICHT ab (best-effort
// Diagnose, ADR-030).
func (s *Server) writeInternalEvent(subject, evType string, data map[string]any) {
	if !s.cfg.AuthEvents {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		s.logger.Error("auth-event kodieren fehlgeschlagen", "type", evType, "err", err)
		return
	}
	c := event.Candidate{Source: authEventSource, Subject: subject, Type: evType, Data: raw}
	written, err := s.store.AppendAuthored([]event.Candidate{c}, nil, "")
	if err != nil {
		s.logger.Error("auth-event schreiben fehlgeschlagen", "type", evType, "subject", subject, "err", err)
		return
	}
	s.metrics.AddEventsWritten(len(written))
	s.recordEventStats(written)
	s.broker.Publish(written)
}

// emitSessionStarted schreibt ein session-started-Event (Login-Äquivalent eines
// sessionlosen Token-Systems): ein Schlüssel wurde nach Offline-Zeit wieder aktiv.
func (s *Server) emitSessionStarted(kid, name string, scopes []auth.Scope) {
	s.writeInternalEvent(subjectAuthSessions+kid, typeSessionStarted, map[string]any{
		"kid":    kid,
		"name":   name,
		"scopes": scopes,
		"at":     time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// emitSessionEnded schreibt ein session-ended-Event für eine vom Sweeper
// geschlossene Presence-Session.
func (s *Server) emitSessionEnded(e activity.Ended) {
	s.writeInternalEvent(subjectAuthSessions+e.KID, typeSessionEnded, map[string]any{
		"kid":             e.KID,
		"name":            e.Name,
		"scopes":          e.Scopes,
		"sessionStarted":  e.SessionStarted.Format(time.RFC3339Nano),
		"lastSeen":        e.LastSeen.Format(time.RFC3339Nano),
		"durationSeconds": int64(e.LastSeen.Sub(e.SessionStarted).Seconds()),
		"reason":          "window-expired",
	})
}

// emitKeyCreated schreibt ein key-created-Event. byKID ist der Admin, der den
// Schlüssel angelegt hat (leer beim Bootstrap-/systemnahen Pfad).
func (s *Server) emitKeyCreated(k auth.Key, byKID string) {
	s.writeInternalEvent(subjectAuthKeys+k.KID, typeKeyCreated, map[string]any{
		"kid":    k.KID,
		"name":   k.Name,
		"scopes": k.Scopes,
		"by":     byKID,
		"at":     time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// emitKeyRevoked schreibt ein key-revoked-Event. byKID ist der widerrufende Admin.
func (s *Server) emitKeyRevoked(kid, byKID string) {
	s.writeInternalEvent(subjectAuthKeys+kid, typeKeyRevoked, map[string]any{
		"kid": kid,
		"by":  byKID,
		"at":  time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// maybeEmitDenied schreibt — nur bei aktiviertem CLIO_AUTH_DENIED_EVENTS und
// rate-limitiert je kid — ein access-denied-Event. Die Rate-Begrenzung verhindert,
// dass wiederholte Fehlversuche (etwa eines Angreifers) den Event-Strom fluten.
func (s *Server) maybeEmitDenied(kid, name string, scopes []auth.Scope, scope auth.Scope, status int) {
	if !s.cfg.AuthEvents || !s.cfg.AuthDeniedEvents {
		return
	}
	if !s.allowDeniedEvent(kid, time.Now().UTC()) {
		return
	}
	s.writeInternalEvent(subjectAuthDenied+kid, typeAccessDenied, map[string]any{
		"kid":           kid,
		"name":          name,
		"scopes":        scopes,
		"requiredScope": string(scope),
		"status":        status,
		"at":            time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// allowDeniedEvent setzt eine einfache Mindestabstand-Drossel je kid: höchstens
// ein access-denied-Event pro Presence-Fenster (mindestens 60s).
func (s *Server) allowDeniedEvent(kid string, now time.Time) bool {
	interval := s.cfg.PresenceWindow
	if interval < time.Minute {
		interval = time.Minute
	}
	s.deniedMu.Lock()
	defer s.deniedMu.Unlock()
	if last, ok := s.deniedLastSeen[kid]; ok && now.Sub(last) < interval {
		return false
	}
	s.deniedLastSeen[kid] = now
	return true
}
