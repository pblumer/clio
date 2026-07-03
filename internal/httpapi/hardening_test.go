package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/config"
)

// TestSecurityHeaders prüft WP-4.5/SEC-M2: die Basis-Sicherheits-Header liegen auf
// jeder Antwort, die strikte Content-Security-Policy nur auf dem Dashboard (/ui) und
// bewusst nicht auf der Swagger-UI (/docs, die Inline-Assets braucht).
func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)

	// Basis-Header auf einer API-Antwort.
	rec := do(t, srv, http.MethodGet, "/api/v1/ping", "", "")
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	// Ohne CLIO_HSTS kein HSTS-Header.
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS unerwartet gesetzt: %q", got)
	}
	// API-Antworten bekommen keine (dashboard-spezifische) CSP.
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP unerwartet auf API-Antwort: %q", got)
	}

	// /ui bekommt eine CSP mit script-src 'self'.
	uiRec := do(t, srv, http.MethodGet, "/ui/", "", "")
	csp := uiRec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("/ui CSP fehlt oder ohne script-src 'self': %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("/ui CSP ohne frame-ancestors 'none': %q", csp)
	}
}

// TestHSTSOptIn prüft, dass CLIO_HSTS den Strict-Transport-Security-Header aktiviert.
func TestHSTSOptIn(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", HSTS: true})
	rec := do(t, srv, http.MethodGet, "/api/v1/ping", "", "")
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=") {
		t.Errorf("HSTS-Header fehlt bei CLIO_HSTS=true: %q", got)
	}
}

// TestInfoOmitsDatabaseFilePath prüft WP-4.5/N3: der interne DB-Dateipfad wird nicht
// mehr über /info preisgegeben.
func TestInfoOmitsDatabaseFilePath(t *testing.T) {
	srv := newTestServer(t)
	rec := do(t, srv, http.MethodGet, "/api/v1/info", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("info status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "databaseFilePath") {
		t.Errorf("/info enthält databaseFilePath (Informationspreisgabe): %s", rec.Body.String())
	}
}

// TestBackupMultiPartitionReturns501 prüft WP-4.5/C-H4: ein Online-Backup bei N>1
// liefert einen klaren 501 statt eines leeren 200 („Backups").
func TestBackupMultiPartitionReturns501(t *testing.T) {
	srv := newPartitionedServer(t, 3)
	rec := do(t, srv, http.MethodGet, "/api/v1/backup", adminToken, "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("backup status bei N>1 = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Error("erwartete eine Fehlerbeschreibung im Body, bekam leeren Body")
	}
}

// TestBackupSinglePartitionOK stellt sicher, dass das Backup bei n=1 unverändert
// funktioniert (ein nicht-leeres bbolt-Artefakt).
func TestBackupSinglePartitionOK(t *testing.T) {
	srv := newTestServer(t)
	// Ein Event, damit das Backup Inhalt hat.
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"s","subject":"/a/1","type":"io.t","data":{}}]}`); rec.Code != http.StatusOK {
		t.Fatalf("write status = %d", rec.Code)
	}
	rec := do(t, srv, http.MethodGet, "/api/v1/backup", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("backup status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("backup-artefakt ist leer")
	}
}
