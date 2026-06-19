package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/auth"
)

// TestExpiredKeyRejected: ein abgelaufener Schlüssel wird wie ein widerrufener
// mit 401 abgewiesen (Usable prüft Status + Ablauf).
func TestExpiredKeyRejected(t *testing.T) {
	srv := newTestServer(t)

	past := time.Now().UTC().Add(-time.Hour)
	expired := auth.Key{
		KID:        "kid_exp001",
		Name:       "expired",
		SecretHash: auth.HashSecret("expiredsecretexpiredsecret012345"),
		Scopes:     []auth.Scope{auth.ScopeRead},
		Status:     auth.StatusActive,
		CreatedAt:  time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt:  &past,
	}
	if err := srv.store.PutKey(expired); err != nil {
		t.Fatal(err)
	}

	tok := "kid_exp001.expiredsecretexpiredsecret012345"
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", tok, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("abgelaufener key status = %d, want 401", rec.Code)
	}

	// Gegenprobe: ein künftiges Ablaufdatum wird akzeptiert.
	future := time.Now().UTC().Add(time.Hour)
	ok := expired
	ok.KID = "kid_exp002"
	ok.ExpiresAt = &future
	if err := srv.store.PutKey(ok); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", "kid_exp002.expiredsecretexpiredsecret012345", ""); rec.Code != http.StatusOK {
		t.Fatalf("künftiger ablauf status = %d, want 200", rec.Code)
	}
}

// TestCreateKeyWithMetadata: Metadaten (expiresAt/owner/...) landen in der
// Liste; ein Ablauf in der Vergangenheit wird mit 400 abgelehnt.
func TestCreateKeyWithMetadata(t *testing.T) {
	srv := newTestServer(t)

	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	body := `{"name":"reporter","scopes":["read"],"owner":"team-x","purpose":"dashboards","expiresAt":"` + future + `"}`
	rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, http.MethodGet, "/api/v1/keys", adminToken, "")
	var resp struct {
		Keys []struct {
			Name      string `json:"name"`
			Owner     string `json:"owner"`
			Purpose   string `json:"purpose"`
			ExpiresAt string `json:"expiresAt"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, k := range resp.Keys {
		if k.Name == "reporter" {
			found = true
			if k.Owner != "team-x" || k.Purpose != "dashboards" || k.ExpiresAt == "" {
				t.Fatalf("metadaten fehlen in liste: %+v", k)
			}
		}
	}
	if !found {
		t.Fatalf("angelegter key nicht in liste: %s", rec.Body.String())
	}

	// Ablauf in der Vergangenheit → 400.
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"bad","scopes":["read"],"expiresAt":"`+past+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("create mit vergangenem ablauf status = %d, want 400", rec.Code)
	}
}

// TestRotateKeyEndpoint: nach Rotation ist der alte Wert ungültig, der neue gilt;
// ein unbekannter kid liefert 404, ein read-only-Key 403.
func TestRotateKeyEndpoint(t *testing.T) {
	srv := newTestServer(t)

	// Einen rotierbaren Read-Key anlegen.
	kid, wire := createKey(t, srv, adminToken, `{"name":"rotateme","scopes":["read"]}`)
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", wire, ""); rec.Code != http.StatusOK {
		t.Fatalf("alter wert status = %d, want 200", rec.Code)
	}

	rec := do(t, srv, http.MethodPost, "/api/v1/keys/"+kid+"/rotate", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var rr struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Secret == "" || rr.Secret == wire {
		t.Fatalf("neuer wert ungültig: %q", rr.Secret)
	}

	// Alter Wert nun 401, neuer 200.
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", wire, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("alter wert nach rotate status = %d, want 401", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", rr.Secret, ""); rec.Code != http.StatusOK {
		t.Fatalf("neuer wert status = %d, want 200", rec.Code)
	}

	// Unbekannter kid → 404.
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys/kid_nope/rotate", adminToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("rotate unbekannt status = %d, want 404", rec.Code)
	}

	// Read-only-Key darf nicht rotieren → 403.
	readTok := seedKey(t, srv.store, "kid_rlro01", "rotreadrotreadrotreadrotread0123", auth.StatusActive, auth.ScopeRead)
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys/"+kid+"/rotate", readTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("rotate mit read-key status = %d, want 403", rec.Code)
	}
}

// TestScopeSeparation deckt die geforderten Scope-Grenzen ab:
// read-only kann nicht schreiben, write-only kann nicht admin.
func TestScopeSeparation(t *testing.T) {
	srv := newTestServer(t)
	readTok := seedKey(t, srv.store, "kid_ronly1", "readonlyreadonlyreadonly01234567", auth.StatusActive, auth.ScopeRead)
	writeTok := seedKey(t, srv.store, "kid_wonly1", "writeonlywriteonlywriteonly012345", auth.StatusActive, auth.ScopeWrite)

	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", readTok,
		`{"events":[{"source":"s","subject":"/a","type":"t"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read-key schreibt: status = %d, want 403", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/keys", writeTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("write-key admin: status = %d, want 403", rec.Code)
	}
	// Gegenprobe: write-key darf schreiben.
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", writeTok,
		`{"events":[{"source":"s","subject":"/a","type":"t"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("write-key schreibt nicht: status = %d, want 200; body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
