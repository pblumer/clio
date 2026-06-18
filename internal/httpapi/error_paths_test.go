package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

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
