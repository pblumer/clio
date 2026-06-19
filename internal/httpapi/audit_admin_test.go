package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

// auditEntries liest GET /api/v1/audit mit dem gegebenen Token.
func auditEntries(t *testing.T, srv *Server, token string) []map[string]any {
	t.Helper()
	rec := do(t, srv, http.MethodGet, "/api/v1/audit", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Entries
}

// TestAuditRecordsAdminAction: eine erfolgreiche Admin-Aktion (Key anlegen)
// erzeugt einen Audit-Eintrag mit Actor, Aktion und Ergebnis — ohne Geheimnis.
func TestAuditRecordsAdminAction(t *testing.T) {
	srv := newTestServer(t)

	kid, wire := createKey(t, srv, adminToken, `{"name":"auditee","scopes":["read"]}`)
	secret := wire[strings.IndexByte(wire, '.')+1:]

	entries := auditEntries(t, srv, adminToken)
	var found bool
	for _, e := range entries {
		if e["action"] == "key.create" && e["target"] == kid {
			found = true
			if e["result"] != "success" {
				t.Fatalf("result = %v, want success", e["result"])
			}
			if e["actorKid"] != testAdminKID {
				t.Fatalf("actorKid = %v, want %s", e["actorKid"], testAdminKID)
			}
		}
		// Kein Geheimnis darf irgendwo im Audit auftauchen.
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret im audit-eintrag: %s", raw)
		}
	}
	if !found {
		t.Fatalf("kein key.create-eintrag gefunden: %+v", entries)
	}
}

// TestAuditRecordsFailedAction: eine fehlgeschlagene Admin-Aktion (revoke eines
// unbekannten kid) ist auditierbar (result=failure).
func TestAuditRecordsFailedAction(t *testing.T) {
	srv := newTestServer(t)

	if rec := do(t, srv, http.MethodPost, "/api/v1/keys/kid_ghost/revoke", adminToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke unbekannt status = %d, want 404", rec.Code)
	}
	entries := auditEntries(t, srv, adminToken)
	var found bool
	for _, e := range entries {
		if e["action"] == "key.revoke" && e["target"] == "kid_ghost" && e["result"] == "failure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kein fehlgeschlagener revoke-eintrag: %+v", entries)
	}
}

// TestAuditReadScopes: lesbar mit `audit` ODER `admin`; ein reiner read-Key wird
// abgewiesen (403), ohne Auth 401.
func TestAuditReadScopes(t *testing.T) {
	srv := newTestServer(t)

	auditTok := seedKey(t, srv.store, "kid_auditor", "auditorauditorauditorauditor0123", auth.StatusActive, auth.ScopeAudit)
	readTok := seedKey(t, srv.store, "kid_readonl", "readonlyreadonlyreadonly01234567", auth.StatusActive, auth.ScopeRead)

	if rec := do(t, srv, http.MethodGet, "/api/v1/audit", auditTok, ""); rec.Code != http.StatusOK {
		t.Fatalf("auditor status = %d, want 200", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/audit", adminToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/audit", readTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read-key status = %d, want 403", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/audit", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ohne auth status = %d, want 401", rec.Code)
	}
}

// TestAuditAuditorCannotWrite: ein reiner audit-Key darf NICHT schreiben oder
// Keys verwalten (least privilege).
func TestAuditAuditorCannotWrite(t *testing.T) {
	srv := newTestServer(t)
	auditTok := seedKey(t, srv.store, "kid_aud2", "auditor2auditor2auditor2auditor20", auth.StatusActive, auth.ScopeAudit)

	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", auditTok,
		`{"events":[{"source":"s","subject":"/a","type":"t"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("audit-key schreibt: status = %d, want 403", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/keys", auditTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("audit-key admin: status = %d, want 403", rec.Code)
	}
}
