package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/store"
)

// TestBackupEndpointAdmin: der Admin-Snapshot ist eine eigenständig öffenbare,
// verify-grüne bbolt-Datei mit derselben Event-Zahl wie der Store (ADR-026).
func TestBackupEndpointAdmin(t *testing.T) {
	srv := newTestServer(t)

	// Ein paar Events schreiben.
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"s","subject":"/a","type":"t"},{"source":"s","subject":"/a","type":"t"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("write status = %d", rec.Code)
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/backup", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("backup status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("leerer backup-body")
	}

	// Den Stream auf Platte legen und offline verifizieren.
	snap := filepath.Join(t.TempDir(), "snap.clio")
	if err := os.WriteFile(snap, rec.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	vr, err := store.VerifyFile(snap, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !vr.OK || vr.Count != 2 {
		t.Fatalf("verify snapshot: ok=%v count=%d", vr.OK, vr.Count)
	}
}

// TestBackupEndpointForbidden: ein reiner Read-Key darf nicht backuppen.
func TestBackupEndpointForbidden(t *testing.T) {
	srv := newTestServer(t)
	readTok := seedKey(t, srv.store, "kid_bkro01", "backupreadbackupreadbackupr01234", auth.StatusActive, auth.ScopeRead)

	if rec := do(t, srv, http.MethodGet, "/api/v1/backup", readTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("backup mit read-key status = %d, want 403", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/backup", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("backup ohne auth status = %d, want 401", rec.Code)
	}
}
