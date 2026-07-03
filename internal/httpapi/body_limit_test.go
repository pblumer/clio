package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/config"
)

// TestBodyLimitRejectsOversize prüft A3/WP-4.3: ein Request-Body über
// cfg.MaxBodyBytes wird mit 413 (Payload Too Large) abgewiesen, statt komplett in
// den Speicher dekodiert zu werden.
func TestBodyLimitRejectsOversize(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", MaxBodyBytes: 1024})

	// Ein write-events-Body deutlich über dem Limit (großes data-Feld).
	big := strings.Repeat("A", 4096)
	body := `{"events":[{"source":"s","subject":"/big/1","type":"io.big","data":{"blob":"` + big + `"}}]}`

	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413)", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestBodyLimitAllowsWithinLimit stellt sicher, dass ein normaler Body unter dem
// Limit unverändert akzeptiert wird.
func TestBodyLimitAllowsWithinLimit(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", MaxBodyBytes: 1 << 20})

	body := `{"events":[{"source":"s","subject":"/ok/1","type":"io.ok","data":{"n":1}}]}`
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Body unter dem Limit)", rec.Code)
	}
}

// TestBodyLimitDisabled prüft, dass MaxBodyBytes<=0 den Riegel abschaltet (großer
// Body wird akzeptiert) — für Deployments mit vorgelagertem, selbst limitierendem
// Proxy.
func TestBodyLimitDisabled(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", MaxBodyBytes: 0})

	big := strings.Repeat("A", 4096)
	body := `{"events":[{"source":"s","subject":"/big/2","type":"io.big","data":{"blob":"` + big + `"}}]}`
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Limit deaktiviert)", rec.Code)
	}
}
