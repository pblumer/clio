package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

// createKey legt über die Admin-Route einen Key an und liefert den
// kid.secret-Leitungswert zurück.
func createKey(t *testing.T, srv *Server, token, body string) (kid, wire string) {
	t.Helper()
	rec := do(t, srv, http.MethodPost, "/api/v1/keys", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create-key status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		KID    string `json:"kid"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("create-key dekodieren: %v", err)
	}
	return resp.KID, resp.Secret
}

func TestCreateListRevokeKey(t *testing.T) {
	srv := newTestServer(t)

	// Anlegen (admin).
	kid, wire := createKey(t, srv, adminToken, `{"name":"ci-reader","scopes":["read"]}`)
	if !strings.HasPrefix(wire, kid+".") {
		t.Fatalf("secret %q sollte mit %q. beginnen", wire, kid)
	}

	// Der neue Key ist SOFORT (ohne Restart) nutzbar.
	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", wire, `{"subject":"/a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("neuer key sollte sofort lesen dürfen, status = %d", rec.Code)
	}
	// ... aber nicht schreiben (nur read-scope).
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", wire, `{"events":[{"source":"s","subject":"/a","type":"t"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-key sollte nicht schreiben dürfen, status = %d", rec.Code)
	}

	// Listen (admin). Enthält den neuen Key, aber niemals Geheimnisse/Hashes.
	rec = do(t, srv, http.MethodGet, "/api/v1/keys", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "secretHash") || strings.Contains(strings.ToLower(raw), "\"secret\"") {
		t.Fatalf("liste enthält geheimnis-felder: %s", raw)
	}
	// Auch der Klartext des secrets darf nirgends auftauchen.
	secretPart := strings.SplitN(wire, ".", 2)[1]
	if strings.Contains(raw, secretPart) {
		t.Fatalf("liste enthält klartext-secret")
	}
	var list struct {
		Keys            []map[string]any `json:"keys"`
		ActiveAdminKeys int              `json:"activeAdminKeys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("list dekodieren: %v", err)
	}
	if len(list.Keys) != 2 { // seed-admin + neuer reader
		t.Fatalf("anzahl keys = %d, want 2", len(list.Keys))
	}
	for _, k := range list.Keys {
		if _, bad := k["secretHash"]; bad {
			t.Fatalf("keyView enthält secretHash: %+v", k)
		}
	}

	// Widerrufen (admin).
	rec = do(t, srv, http.MethodPost, "/api/v1/keys/"+kid+"/revoke", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d", rec.Code)
	}
	// Nach dem Widerruf liefert der Key 401.
	rec = do(t, srv, http.MethodPost, "/api/v1/read-events", wire, `{"subject":"/a"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("widerrufener key status = %d, want 401", rec.Code)
	}

	// Unbekannter kid -> 404.
	rec = do(t, srv, http.MethodPost, "/api/v1/keys/kid_gibtsnicht/revoke", adminToken, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoke unbekannt status = %d, want 404", rec.Code)
	}
}

// TestKeyRoutesRequireAdmin: read-/write-Keys bekommen auf allen drei
// Admin-Routen 403.
func TestKeyRoutesRequireAdmin(t *testing.T) {
	srv := newTestServer(t)
	readTok := seedKey(t, srv.store, "kid_rdonly0", "rdonlyrdonlyrdonlyrdonly01234567", auth.StatusActive, auth.ScopeRead)
	writeTok := seedKey(t, srv.store, "kid_wronly0", "wronlywronlywronlywronly01234567", auth.StatusActive, auth.ScopeRead, auth.ScopeWrite)

	for _, tok := range []string{readTok, writeTok} {
		if rec := do(t, srv, http.MethodPost, "/api/v1/keys", tok, `{"name":"x","scopes":["read"]}`); rec.Code != http.StatusForbidden {
			t.Fatalf("create mit non-admin status = %d, want 403", rec.Code)
		}
		if rec := do(t, srv, http.MethodGet, "/api/v1/keys", tok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("list mit non-admin status = %d, want 403", rec.Code)
		}
		if rec := do(t, srv, http.MethodPost, "/api/v1/keys/kid_x/revoke", tok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("revoke mit non-admin status = %d, want 403", rec.Code)
		}
	}
}

// TestCreateKeyValidation: fehlender Name / leere oder ungültige Scopes -> 400.
func TestCreateKeyValidation(t *testing.T) {
	srv := newTestServer(t)
	cases := []string{
		`{"scopes":["read"]}`,           // name fehlt
		`{"name":"x"}`,                  // scopes fehlt
		`{"name":"x","scopes":[]}`,      // scopes leer
		`{"name":"x","scopes":["god"]}`, // ungültiger scope
	}
	for _, body := range cases {
		if rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestListKeysLastAdminWarning: bei genau einem aktiven Admin-Key warnt die
// Liste; der Widerruf des letzten Admins ist erlaubt (kein harter Block) und
// liefert ebenfalls eine Warnung.
func TestListKeysLastAdminWarning(t *testing.T) {
	srv := newTestServer(t) // genau ein Admin-Key (Seed)

	rec := do(t, srv, http.MethodGet, "/api/v1/keys", adminToken, "")
	var list map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list["activeAdminKeys"].(float64) != 1 {
		t.Fatalf("activeAdminKeys = %v, want 1", list["activeAdminKeys"])
	}
	if _, ok := list["warning"]; !ok {
		t.Fatal("erwartete warnung bei letztem aktiven admin-key")
	}

	// Den letzten Admin widerrufen ist erlaubt und warnt.
	rec = do(t, srv, http.MethodPost, "/api/v1/keys/"+testAdminKID+"/revoke", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke letzter admin status = %d, want 200 (kein harter block)", rec.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp["warning"]; !ok {
		t.Fatal("erwartete warnung beim widerruf des letzten admins")
	}
}
