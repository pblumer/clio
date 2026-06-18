package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

// seedSourceKey legt einen aktiven write-Key mit bekanntem kid/secret und den
// angegebenen erlaubten Sources an (ADR-026) und liefert den Leitungswert.
func seedSourceKey(t *testing.T, st *store.Store, kid, secret string, allowed ...string) string {
	t.Helper()
	k := auth.Key{
		KID:            kid,
		Name:           kid,
		SecretHash:     auth.HashSecret(secret),
		Scopes:         []auth.Scope{auth.ScopeRead, auth.ScopeWrite},
		Status:         auth.StatusActive,
		CreatedAt:      time.Now().UTC(),
		AllowedSources: allowed,
	}
	if err := st.PutKey(k); err != nil {
		t.Fatalf("seed source-key %q: %v", kid, err)
	}
	return kid + "." + secret
}

// newSourceBindingServer baut einen Server mit dem Standard-Admin-Key
// (uneingeschränkt) plus zwei quellgebundenen write-Keys: einer mit genau einer
// erlaubten Source, einer mit mehreren.
func newSourceBindingServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedKey(t, st, testAdminKID, testAdminSecret, auth.StatusActive, auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin)
	single := seedSourceKey(t, st, "kid_single0", "singlesecretsinglesecret00000000", "svc-orders")
	multi := seedSourceKey(t, st, "kid_multi00", "multisecretmultisecret0000000000", "svc-a", "svc-b")
	return New(config.Config{Addr: ":0"}, st, nil), single, multi
}

func TestWriteSourceBindingSingleSource(t *testing.T) {
	srv, single, _ := newSourceBindingServer(t)

	// source weglassen -> Server setzt die einzige erlaubte Source.
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", single,
		`{"events":[{"subject":"/o/1","type":"placed"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ohne source: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 1 || got[0].Source != "svc-orders" {
		t.Fatalf("ohne source: aufgezeichnete source = %+v, want svc-orders", got)
	}

	// passende source -> erlaubt.
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", single,
		`{"events":[{"source":"svc-orders","subject":"/o/2","type":"placed"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("passende source: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// abweichende source -> 403 (kein stilles Überschreiben).
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", single,
		`{"events":[{"source":"svc-fremd","subject":"/o/3","type":"placed"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("abweichende source: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteSourceBindingMultiSource(t *testing.T) {
	srv, _, multi := newSourceBindingServer(t)

	// fehlende source -> 400 (bei mehreren erlaubten Sources Pflicht).
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", multi,
		`{"events":[{"subject":"/o/1","type":"placed"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("fehlende source: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// enthaltene source -> erlaubt, unverändert aufgezeichnet.
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", multi,
		`{"events":[{"source":"svc-b","subject":"/o/2","type":"placed"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enthaltene source: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 1 || got[0].Source != "svc-b" {
		t.Fatalf("enthaltene source: aufgezeichnet = %+v, want svc-b", got)
	}

	// nicht enthaltene source -> 403.
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", multi,
		`{"events":[{"source":"svc-c","subject":"/o/3","type":"placed"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("nicht enthaltene source: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestWriteSourceBindingUnrestricted stellt sicher, dass ein Schlüssel ohne
// allowedSources (z. B. Bootstrap-Admin) die client-gesetzte source unverändert
// übernimmt — abwärtskompatibel zum Verhalten vor ADR-026.
func TestWriteSourceBindingUnrestricted(t *testing.T) {
	srv, _, _ := newSourceBindingServer(t)

	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"beliebige-quelle","subject":"/o/1","type":"placed"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unrestricted: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 1 || got[0].Source != "beliebige-quelle" {
		t.Fatalf("unrestricted: aufgezeichnet = %+v, want beliebige-quelle", got)
	}

	// Ohne erlaubte Sources bleibt source weiterhin pflicht (Validate): leeres
	// source -> 400.
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"subject":"/o/2","type":"placed"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unrestricted ohne source: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateKeyWithAllowedSources prüft den Admin-Flow: Anlegen mit
// allowedSources, Auslesen in der Liste, und dass leere Einträge 400 ergeben.
func TestCreateKeyWithAllowedSources(t *testing.T) {
	srv := newTestServer(t)

	rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"gateway","scopes":["write"],"allowedSources":["svc-a","svc-b"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		KID            string   `json:"kid"`
		AllowedSources []string `json:"allowedSources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create-antwort dekodieren: %v", err)
	}
	if len(created.AllowedSources) != 2 || created.AllowedSources[0] != "svc-a" {
		t.Fatalf("create: allowedSources = %v", created.AllowedSources)
	}

	// In der Liste sichtbar.
	rec = do(t, srv, http.MethodGet, "/api/v1/keys", adminToken, "")
	var list struct {
		Keys []keyView `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("list-antwort dekodieren: %v", err)
	}
	var found bool
	for _, k := range list.Keys {
		if k.KID == created.KID {
			found = true
			if len(k.AllowedSources) != 2 {
				t.Fatalf("list: allowedSources = %v, want 2", k.AllowedSources)
			}
		}
	}
	if !found {
		t.Fatalf("angelegter key nicht in der liste")
	}

	// Leerer Eintrag -> 400.
	rec = do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"bad","scopes":["write"],"allowedSources":["svc-a",""]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("leerer allowedSources-eintrag: status = %d, want 400", rec.Code)
	}
}
