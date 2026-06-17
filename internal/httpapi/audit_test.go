package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

// safeBuffer ist ein nebenläufigkeitssicherer Puffer für den slog-Handler
// (observe/instrument können aus mehreren Goroutinen loggen).
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newAuditServer baut einen Server, dessen Logs in buf landen, und seedet einen
// Admin-Key.
func newAuditServer(t *testing.T) (*Server, *safeBuffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedKey(t, st, testAdminKID, testAdminSecret, auth.StatusActive, auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin)
	buf := &safeBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	return New(config.Config{Addr: ":0"}, st, logger), buf
}

// auditLines liefert alle geloggten audit-Zeilen (msg=="audit") als Maps.
func auditLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue // andere Zeilen (z. B. request-log) ignorieren
		}
		if m["msg"] == "audit" {
			out = append(out, m)
		}
	}
	return out
}

func TestAuditAllowLogged(t *testing.T) {
	srv, buf := newAuditServer(t)

	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", adminToken, `{"subject":"/a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	lines := auditLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("anzahl audit-zeilen = %d, want 1", len(lines))
	}
	a := lines[0]
	if a["decision"] != "allow" {
		t.Fatalf("decision = %v, want allow", a["decision"])
	}
	if a["kid"] != testAdminKID {
		t.Fatalf("kid = %v, want %s (kid muss bei erfolg geloggt werden)", a["kid"], testAdminKID)
	}
	if a["scope"] != "read" {
		t.Fatalf("scope = %v, want read", a["scope"])
	}
	if a["method"] != "POST" || a["path"] != "/api/v1/read-events" {
		t.Fatalf("method/path falsch: %v %v", a["method"], a["path"])
	}
}

func TestAuditDenyLogged(t *testing.T) {
	srv, buf := newAuditServer(t)
	readTok := seedKey(t, srv.store, "kid_aud001", "auditreadauditreadauditread012345", auth.StatusActive, auth.ScopeRead)

	// 403: gültiger read-Key auf write-Route.
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", readTok, `{"events":[{"source":"s","subject":"/a","type":"t"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// 401: unbekannter kid.
	if rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "kid_weg.egal", `{"subject":"/a"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// 401: gar kein Header.
	if rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "", `{"subject":"/a"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	lines := auditLines(t, buf.String())
	if len(lines) != 3 {
		t.Fatalf("anzahl audit-zeilen = %d, want 3", len(lines))
	}

	// 403-Zeile: decision=deny, status=403, kid des read-Keys vorhanden.
	var got403, got401Unknown, got401NoHeader bool
	for _, a := range lines {
		if a["decision"] != "deny" {
			t.Fatalf("unerwartete decision: %v", a["decision"])
		}
		switch a["status"].(float64) {
		case 403:
			got403 = true
			if a["kid"] != "kid_aud001" {
				t.Fatalf("403 kid = %v, want kid_aud001", a["kid"])
			}
		case 401:
			if a["kid"] == "kid_weg" {
				got401Unknown = true
			}
			if _, hasKID := a["kid"]; !hasKID {
				got401NoHeader = true // kein Header -> kid weggelassen
			}
		}
	}
	if !got403 || !got401Unknown || !got401NoHeader {
		t.Fatalf("erwartete deny-zeilen fehlen: 403=%v 401unknown=%v 401noheader=%v", got403, got401Unknown, got401NoHeader)
	}
}

// TestAuditNoSecretInLog stellt sicher, dass weder Klartext-Geheimnis noch Hash
// jemals im Log auftauchen.
func TestAuditNoSecretInLog(t *testing.T) {
	srv, buf := newAuditServer(t)

	// Erfolgreicher Zugriff plus ein falsches Geheimnis.
	do(t, srv, http.MethodPost, "/api/v1/read-events", adminToken, `{"subject":"/a"}`)
	do(t, srv, http.MethodPost, "/api/v1/read-events", testAdminKID+".falschesgeheimnis", `{"subject":"/a"}`)

	raw := buf.String()
	if strings.Contains(raw, testAdminSecret) {
		t.Fatal("klartext-secret im log gefunden")
	}
	if strings.Contains(raw, auth.HashSecret(testAdminSecret)) {
		t.Fatal("secret-hash im log gefunden")
	}
	if strings.Contains(raw, "falschesgeheimnis") {
		t.Fatal("falsches geheimnis (klartext) im log gefunden")
	}
}
