package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/pblumer/clio/internal/activity"
	"github.com/pblumer/clio/internal/auth"
)

// categoryForScope bildet den geforderten Routen-Scope auf die Aktivitäts-
// Kategorie der Presence-Registry ab (ADR-030).
func categoryForScope(scope auth.Scope) activity.Category {
	switch scope {
	case auth.ScopeWrite:
		return activity.CategoryWrite
	case auth.ScopeAdmin:
		return activity.CategoryAdmin
	default:
		return activity.CategoryRead
	}
}

// scopeStrings übersetzt eine auth.Scope-Liste in Strings (für die
// abhängigkeitsfreie Aktivitäts-Registry).
func scopeStrings(scopes []auth.Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// contextKey ist der private Schlüsseltyp für Werte im Request-Context.
type contextKey int

const identityContextKey contextKey = iota

// withIdentity legt die authentifizierte Identität in den Context.
func withIdentity(ctx context.Context, id auth.Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

// identityFromContext liefert die authentifizierte Identität eines Requests
// (für Handler und Audit-Log). ok ist false bei nicht authentifizierten
// Requests (offene Routen).
func identityFromContext(r *http.Request) (auth.Identity, bool) {
	id, ok := r.Context().Value(identityContextKey).(auth.Identity)
	return id, ok
}

// authorKID liefert den kid, der als Urheberschaft auf neu geschriebene Events
// gestempelt wird (ADR-025). Leer, wenn die Event-Urheberschaft deaktiviert ist
// (CLIO_EVENT_AUTHORSHIP) oder keine Identität vorliegt — dann bleibt das
// Verhalten byte-identisch zum bisherigen Schreibpfad.
func (s *Server) authorKID(r *http.Request) string {
	if !s.eventAuthorship {
		return ""
	}
	if id, ok := identityFromContext(r); ok {
		return id.KID
	}
	return ""
}

// dummyHash ist ein gültiger SHA-256-Hex-Hash, gegen den auch bei
// unbekanntem/fehlendem kid zeitkonstant verglichen wird. So gleicht sich die
// Antwortzeit an und verrät nicht über ein Timing-Orakel, ob ein kid existiert
// (Sicherheits-Checkliste §3).
var dummyHash = auth.HashSecret("clio:auth:nonexistent-key-timing-placeholder")

// requireScope umschließt einen Handler mit der scope-bewussten
// Schlüsselbund-Authentifizierung (ADR-025). Ablauf:
//  1. Authorization-Header als `Bearer kid.secret` zerlegen.
//  2. Schlüssel über kid laden; fehlt er (oder kid-Fehlformat) → 401.
//  3. Geheimnis zeitkonstant gegen den gespeicherten Hash prüfen → 401 bei
//     Ungleichheit. Der Vergleich läuft IMMER (auch bei unbekanntem kid gegen
//     dummyHash), um kein Timing-Orakel über die kid-Existenz zu öffnen.
//  4. Status != active (widerrufen) → 401.
//  5. Fehlender Scope → 403 (klar getrennt von 401).
//  6. Identität in den Context legen und next aufrufen.
func (s *Server) requireScope(scope auth.Scope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kid, secret, parsed := auth.ParseBearer(r.Header.Get("Authorization"))

		var key auth.Key
		var found bool
		if parsed {
			k, ok, err := s.store.GetKey(kid)
			if err != nil {
				// Echter Infrastrukturfehler (z. B. Store nicht verfügbar) ist ein
				// Serverfehler, kein Authentifizierungsergebnis → 500. Ein bloß
				// unbekannter kid liefert err==nil, ok==false und wird unten zu 401.
				s.logger.Error("auth: key-lookup fehlgeschlagen", "kid", kid, "err", err)
				writeError(w, http.StatusInternalServerError, "interner fehler bei der authentifizierung")
				return
			}
			if ok {
				key, found = k, true
			}
		}

		// Zeitkonstanter Vergleich — auch bei nicht gefundenem kid gegen einen
		// Dummy-Hash gleicher Länge (kein Timing-Leak über die Existenz, §3).
		expectedHash := dummyHash
		if found {
			expectedHash = key.SecretHash
		}
		secretOK := subtle.ConstantTimeCompare([]byte(auth.HashSecret(secret)), []byte(expectedHash)) == 1

		// auditKID ist der (nicht-geheime) übermittelte kid, sofern der Header
		// überhaupt zerlegbar war — auch bei Ablehnung nützlich fürs Audit.
		auditKID := ""
		if parsed {
			auditKID = kid
		}

		// 401: kein gültiger Bearer, unbekannter kid, falsches Geheimnis oder
		// widerrufener Schlüssel. Bewusst kein Name im Log (uniformes 401).
		if !parsed || !found || !secretOK || !key.Active() {
			s.auditDecision(r, scope, "deny", http.StatusUnauthorized, auditKID, "")
			s.metrics.ObserveAuthDecision(string(scope), "deny")
			// Aktivität/Denial nur für BEKANNTE Schlüssel verbuchen (found): so kann
			// ein 401 mit beliebigem, unbekanntem kid die Registry nicht mit
			// Müll-Einträgen aufblähen (ADR-030, Review-Gate).
			if found {
				s.activity.Record(key.KID, key.Name, scopeStrings(key.Scopes), categoryForScope(scope), false, time.Now().UTC())
				s.maybeEmitDenied(key.KID, key.Name, key.Scopes, scope, http.StatusUnauthorized)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// 403: gültig authentifiziert, aber der Schlüssel trägt den nötigen Scope
		// nicht. Bewusst von 401 getrennt (ADR-025).
		if !key.HasScope(scope) {
			s.auditDecision(r, scope, "deny", http.StatusForbidden, key.KID, key.Name)
			s.metrics.ObserveAuthDecision(string(scope), "deny")
			s.activity.Record(key.KID, key.Name, scopeStrings(key.Scopes), categoryForScope(scope), false, time.Now().UTC())
			s.maybeEmitDenied(key.KID, key.Name, key.Scopes, scope, http.StatusForbidden)
			writeError(w, http.StatusForbidden, "forbidden: scope "+string(scope)+" erforderlich")
			return
		}

		s.auditDecision(r, scope, "allow", http.StatusOK, key.KID, key.Name)
		s.metrics.ObserveAuthDecision(string(scope), "allow")
		// Aktivität verbuchen; ein Übergang offline→online löst (opt-in) ein
		// session-started-Event aus (ADR-030).
		if started := s.activity.Record(key.KID, key.Name, scopeStrings(key.Scopes), categoryForScope(scope), true, time.Now().UTC()); started {
			s.emitSessionStarted(key.KID, key.Name, key.Scopes)
		}
		ident := auth.Identity{KID: key.KID, Name: key.Name, Scopes: key.Scopes}
		next(w, r.WithContext(withIdentity(r.Context(), ident)))
	}
}
