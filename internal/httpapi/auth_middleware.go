package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
)

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

// resolveEventSources bindet die `source` jedes Candidate an die im Token
// hinterlegten erlaubten Sources (ADR-026, Entscheidung 2) und mutiert die Liste
// in place: Erlaubt das Token genau eine Source und der Client lässt sie weg,
// wird sie gesetzt; eine abweichende oder nicht erlaubte Source wird hart
// abgelehnt. Bei einer Verletzung liefert ok=false plus den passenden
// HTTP-Status und eine Meldung (400, wenn die Pflicht-source fehlt; 403, wenn die
// Source nicht autorisiert ist). Ohne Identität (offene Route — auf dem
// Write-Pfad nicht erreichbar) bleibt das Verhalten unverändert.
func (s *Server) resolveEventSources(r *http.Request, candidates []event.Candidate) (status int, msg string, ok bool) {
	id, hasID := identityFromContext(r)
	if !hasID {
		return 0, "", true
	}
	for i := range candidates {
		resolved, err := id.ResolveSource(candidates[i].Source)
		if err != nil {
			prefix := "events[" + strconv.Itoa(i) + "]: "
			if errors.Is(err, auth.ErrSourceRequired) {
				return http.StatusBadRequest, prefix + err.Error(), false
			}
			// ErrSourceNotAllowed (und jeder andere Bindungsfehler): authentifiziert,
			// aber für diese Source nicht autorisiert.
			s.logger.Warn("write abgelehnt: source nicht autorisiert",
				"kid", id.KID, "requestedSource", candidates[i].Source, "allowedSources", id.AllowedSources)
			return http.StatusForbidden, prefix + err.Error(), false
		}
		candidates[i].Source = resolved
	}
	return 0, "", true
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
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// 403: gültig authentifiziert, aber der Schlüssel trägt den nötigen Scope
		// nicht. Bewusst von 401 getrennt (ADR-025).
		if !key.HasScope(scope) {
			s.auditDecision(r, scope, "deny", http.StatusForbidden, key.KID, key.Name)
			writeError(w, http.StatusForbidden, "forbidden: scope "+string(scope)+" erforderlich")
			return
		}

		s.auditDecision(r, scope, "allow", http.StatusOK, key.KID, key.Name)
		ident := auth.Identity{KID: key.KID, Name: key.Name, Scopes: key.Scopes, AllowedSources: key.AllowedSources}
		next(w, r.WithContext(withIdentity(r.Context(), ident)))
	}
}
