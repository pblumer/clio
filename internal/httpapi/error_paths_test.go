package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

// closedStoreServer baut einen Dev-Server, dessen Store sofort geschlossen wird:
// Jede nachfolgende DB-Operation schlägt fehl ("database not open"). So lassen
// sich die internen Fehlerpfade (HTTP 500) der Handler isoliert prüfen.
func closedStoreServer(t *testing.T) *Server {
	t.Helper()
	srv := newDevServer(t)
	if err := srv.store.Close(); err != nil {
		t.Fatalf("store schließen: %v", err)
	}
	return srv
}

// callHandler ruft einen Handler DIREKT auf (ohne Auth-Middleware). Das ist der
// saubere Weg, den internen Store-Fehlerzweig eines Handlers zu prüfen: Der
// Umweg über requireScope scheitert bei geschlossenem Store bereits am
// Key-Lookup und erreicht den Handler nie.
func callHandler(h http.HandlerFunc, method, target string, pathVals map[string]string, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	// Direkter Aufruf umgeht die Auth-Middleware; die Handler setzen jedoch eine
	// vorhandene Identität voraus (ADR-033 Subject-Prüfung). Eine global
	// berechtigte Identität einsetzen, damit der interne Store-Fehlerpfad geprüft
	// werden kann und nicht schon die Subject-Autorisierung greift.
	r = r.WithContext(withIdentity(r.Context(), auth.Identity{
		KID: "kid_test01", Name: "test",
		Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin, auth.ScopeAudit},
	}))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// TestHandlerInternalErrors deckt die internen Fehlerpfade (HTTP 500) der
// lesenden/schreibenden Handler ab: Bei geschlossenem Store muss jede
// DB-gestützte Route mit 500 (problem+json) antworten — statt zu panicken oder
// einen Erfolg vorzutäuschen. Die Handler werden direkt aufgerufen, da die
// Auth-Middleware bei geschlossenem Store schon vorher 500 liefert (siehe
// TestAuthStoreLookupError) und den Handler sonst nie erreicht.
func TestHandlerInternalErrors(t *testing.T) {
	srv := closedStoreServer(t)

	cases := []struct {
		name     string
		handler  http.HandlerFunc
		method   string
		target   string
		pathVals map[string]string
		body     string
	}{
		{"info", srv.handleInfo, http.MethodGet, "/", nil, ""},
		{"verify", srv.handleVerify, http.MethodGet, "/", nil, ""},
		{"read-event-types", srv.handleReadEventTypes, http.MethodGet, "/", nil, ""},
		{"read-event-schema", srv.handleReadEventSchema, http.MethodGet, "/?type=foo", nil, ""},
		{"read-subjects", srv.handleReadSubjects, http.MethodGet, "/", nil, ""},
		{"subject-children", srv.handleReadSubjects, http.MethodGet, "/?children=/", nil, ""},
		{"register-event-schema", srv.handleRegisterEventSchema, http.MethodPost, "/", nil, `{"type":"order.placed","schema":{"type":"object"}}`},
		{"list-keys", srv.handleListKeys, http.MethodGet, "/", nil, ""},
		{"revoke-key", srv.handleRevokeKey, http.MethodPost, "/", map[string]string{"kid": "kid_irgendwas"}, ""},
		{"create-key", srv.handleCreateKey, http.MethodPost, "/", nil, `{"name":"x","scopes":["read"]}`},
		{"dev-reset", srv.handleDevReset, http.MethodPost, "/", nil, ""},
		{"write-events", srv.handleWriteEvents, http.MethodPost, "/", nil, `{"events":[{"source":"s","subject":"/a","type":"t"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := callHandler(tc.handler, tc.method, tc.target, tc.pathVals, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
				t.Fatalf("Content-Type = %q, want problem+json", ct)
			}
		})
	}
}

// TestReadEventsStreamErrorStays200 deckt den Streaming-Fehlerpfad von doRead ab:
// Der 200-Header geht raus, BEVOR der Lesefehler (geschlossener Store) auftritt,
// daher bleibt der Status 200 und der Fehler wird nur geloggt (siehe
// doRead-Kommentar). Direkter Aufruf, um die Auth-Middleware zu umgehen.
func TestReadEventsStreamErrorStays200(t *testing.T) {
	srv := closedStoreServer(t)
	rec := callHandler(srv.handleReadEvents, http.MethodPost, "/", nil, `{"subject":"/a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stream bereits begonnen)", rec.Code)
	}
}

// TestAuthStoreLookupError deckt den Infrastruktur-Fehlerpfad der
// Schlüsselbund-Authentifizierung ab (requireScope): Schlägt der Key-Lookup im
// Store fehl (hier: Store geschlossen -> "database not open"), ist das ein
// Serverfehler und muss als 500 (problem+json) quittiert werden — bewusst NICHT
// als 401, denn ein bloß unbekannter kid ist etwas anderes als ein nicht
// erreichbarer Store. Das Schließen des bbolt-Stores ist der einfachste
// reproduzierbare Weg, den Lookup fehlschlagen zu lassen.
func TestAuthStoreLookupError(t *testing.T) {
	srv := newTestServer(t)
	// Store schließen -> ab jetzt schlägt GetKey im Auth-Middleware fehl. Der
	// Cleanup von newTestServer schließt erneut (harmlos, Fehler dort ignoriert).
	if err := srv.store.Close(); err != nil {
		t.Fatalf("store schließen: %v", err)
	}

	// Eine beliebige scope-geschützte Route genügt; der Fehler entsteht vor dem
	// Handler im Middleware-Lookup. Ein gültig FORMATIERTES Token ist nötig,
	// damit überhaupt ein Lookup versucht wird (sonst 401 ohne Store-Zugriff).
	rec := do(t, srv, http.MethodGet, "/api/v1/info", adminToken, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("Content-Type = %q, want problem+json", ct)
	}
}

// TestAuthMalformedTokenNoStoreLookup ergänzt den Gegenpol: Ein nicht
// zerlegbarer Header (kein `kid.secret`) darf gar nicht erst im Store
// nachschlagen und ergibt 401 — auch bei geschlossenem Store. So ist belegt,
// dass der 500-Pfad oben wirklich am Lookup hängt und nicht an irgendeinem
// früheren Store-Zugriff.
func TestAuthMalformedTokenNoStoreLookup(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.store.Close(); err != nil {
		t.Fatalf("store schließen: %v", err)
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/info", "kein-punkt-also-unzerlegbar", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestDevBulkImportBadRequests deckt die Validierungszweige des Bulk-Import-
// Handlers ab, die im offenen Startfenster (also vor close-bulk-import)
// erreichbar sind: kaputtes JSON, leere Event-Liste, ein Event das Validate()
// nicht besteht und eine fehlerhafte Precondition — jeweils 400.
func TestDevBulkImportBadRequests(t *testing.T) {
	srv := newDevServer(t) // Fenster ist direkt nach Start offen

	const path = "/api/v1/dev/bulk-import-events"
	cases := []struct {
		name string
		body string
	}{
		{"kaputtes json", `{nicht json`},
		{"leere event-liste", `{"events":[]}`},
		{"event ohne source", `{"events":[{"subject":"/a","type":"t"}]}`},
		{"event subject ohne slash", `{"events":[{"source":"s","subject":"a","type":"t"}]}`},
		{"fehlerhafte precondition", `{"events":[{"source":"s","subject":"/a","type":"t"}],"preconditions":[{"type":"isMagic","payload":{"subject":"/a"}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, path, adminToken, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDevBulkImportPreconditionFailed deckt den Konflikt-Zweig (409) des
// Bulk-Imports ab: eine erfüllbare, aber NICHT erfüllte Precondition
// (isSubjectOnEventId gegen ein jungfräuliches Subject) muss den Import mit 409
// ablehnen, ohne dass etwas geschrieben wird.
func TestDevBulkImportPreconditionFailed(t *testing.T) {
	srv := newDevServer(t)

	body := `{"events":[{"source":"s","subject":"/p","type":"t"}],` +
		`"preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/p","eventId":"99"}}]}`
	rec := do(t, srv, http.MethodPost, "/api/v1/dev/bulk-import-events", adminToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMetricsDegradesOnStoreError stellt sicher, dass /metrics auch bei
// Store-Fehlern erreichbar bleibt (200) und die nicht ermittelbaren Werte als
// Sentinel (-1) ausweist, statt den Scrape mit 500 zu quittieren. /metrics ist
// bewusst ohne Auth, daher erreicht der Request den Handler trotz geschlossenem
// Store und nimmt dessen Fehlerzweige (Count/Stats/DiskUsage) mit.
func TestMetricsDegradesOnStoreError(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.store.Close(); err != nil {
		t.Fatalf("store schließen: %v", err)
	}

	rec := do(t, srv, http.MethodGet, "/metrics", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradiert)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "clio_") {
		t.Fatalf("metrics-body wirkt leer: %s", rec.Body.String())
	}
}
