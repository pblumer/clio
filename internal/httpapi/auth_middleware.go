package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/clio/internal/auth"
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

// dummyHash ist ein gültiger SHA-256-Hex-Hash, gegen den auch bei
// unbekanntem/fehlendem kid zeitkonstant verglichen wird. So gleicht sich die
// Antwortzeit an und verrät nicht über ein Timing-Orakel, ob ein kid existiert
// (Sicherheits-Checkliste §3).
var dummyHash = auth.HashSecret("clio:auth:nonexistent-key-timing-placeholder")

// authenticate führt die scope-unabhängigen Schritte 1–4 aus:
//  1. Authorization-Header als `Bearer kid.secret` zerlegen.
//  2. Schlüssel über kid laden; fehlt er (oder kid-Fehlformat) → 401.
//  3. Geheimnis zeitkonstant gegen den gespeicherten Hash prüfen → 401 bei
//     Ungleichheit. Der Vergleich läuft IMMER (auch bei unbekanntem kid gegen
//     dummyHash), um kein Timing-Orakel über die kid-Existenz zu öffnen.
//  4. Status != active (widerrufen) oder abgelaufen → 401.
//
// Bei Erfolg liefert es den Schlüssel und ok=true. Andernfalls hat es die Antwort
// bereits geschrieben (401 oder 500) und die Ablehnung auditiert; ok=false.
// scopeLabel ist nur das Label fürs Audit der 401-Ablehnung.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request, scopeLabel string) (auth.Key, bool) {
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
			return auth.Key{}, false
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

	auditKID := ""
	if parsed {
		auditKID = kid
	}

	// 401: kein gültiger Bearer, unbekannter kid, falsches Geheimnis,
	// widerrufener ODER abgelaufener Schlüssel (Usable prüft Status + Ablauf).
	if !parsed || !found || !secretOK || !key.Usable(time.Now().UTC()) {
		s.auditDecision(r, auth.Scope(scopeLabel), "deny", http.StatusUnauthorized, auditKID, "")
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return auth.Key{}, false
	}
	return key, true
}

// requireScope umschließt einen Handler mit der scope-bewussten
// Schlüsselbund-Authentifizierung (ADR-025): Authentifizierung (siehe
// authenticate), dann Scope-Prüfung (fehlender Scope → 403, klar getrennt von
// 401), schließlich Identität in den Context legen und next aufrufen.
func (s *Server) requireScope(scope auth.Scope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.authenticate(w, r, string(scope))
		if !ok {
			return
		}
		if !key.HasScope(scope) {
			s.auditDecision(r, scope, "deny", http.StatusForbidden, key.KID, key.Name)
			writeError(w, http.StatusForbidden, "forbidden: scope "+string(scope)+" erforderlich")
			return
		}
		s.auditDecision(r, scope, "allow", http.StatusOK, key.KID, key.Name)
		ident := auth.Identity{KID: key.KID, Name: key.Name, Scopes: key.Scopes}
		next(w, r.WithContext(withIdentity(r.Context(), ident)))
	}
}

// requireAnyScope wie requireScope, akzeptiert aber jeden Schlüssel, der MINDESTENS
// einen der scopes trägt (ADR-032: das Audit-Log liest, wer `audit` ODER `admin`
// hat). Genau ein scope verhält sich identisch zu requireScope.
func (s *Server) requireAnyScope(scopes []auth.Scope, next http.HandlerFunc) http.HandlerFunc {
	label := joinScopes(scopes)
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.authenticate(w, r, label)
		if !ok {
			return
		}
		if !hasAnyScope(key, scopes) {
			s.auditDecision(r, auth.Scope(label), "deny", http.StatusForbidden, key.KID, key.Name)
			writeError(w, http.StatusForbidden, "forbidden: einer der scopes "+label+" erforderlich")
			return
		}
		s.auditDecision(r, auth.Scope(label), "allow", http.StatusOK, key.KID, key.Name)
		ident := auth.Identity{KID: key.KID, Name: key.Name, Scopes: key.Scopes}
		next(w, r.WithContext(withIdentity(r.Context(), ident)))
	}
}

// hasAnyScope meldet, ob der Schlüssel mindestens einen der scopes trägt.
func hasAnyScope(key auth.Key, scopes []auth.Scope) bool {
	for _, sc := range scopes {
		if key.HasScope(sc) {
			return true
		}
	}
	return false
}

// joinScopes baut ein Label "a|b" für Logs/Fehlermeldungen.
func joinScopes(scopes []auth.Scope) string {
	parts := make([]string, len(scopes))
	for i, sc := range scopes {
		parts[i] = string(sc)
	}
	return strings.Join(parts, "|")
}
