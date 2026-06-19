package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/activity"
	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
)

// activityResponse spiegelt die JSON-Antwort von GET /api/v1/activity.
type activityResponse struct {
	OnlineCount int                 `json:"onlineCount"`
	AuthEvents  bool                `json:"authEvents"`
	Keys        []activity.Snapshot `json:"keys"`
}

func decodeActivity(t *testing.T, body string) activityResponse {
	t.Helper()
	var ar activityResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&ar); err != nil {
		t.Fatalf("activity-antwort dekodieren: %v", err)
	}
	return ar
}

// readEventSubjects liest die Events eines Subjects (NDJSON) und liefert die
// Typen je Zeile.
func readEventTypes(t *testing.T, srv *Server, subject string, recursive bool) []string {
	t.Helper()
	body := `{"subject":"` + subject + `","recursive":` + boolStr(recursive) + `}`
	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-events status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var types []string
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("event dekodieren: %v (zeile %q)", err, line)
		}
		types = append(types, ev.Type)
	}
	return types
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestActivityEndpoint_RequiresAdmin(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", PresenceWindow: time.Minute})
	readTok := seedKey(t, srv.store, "kid_reader1", "readersecretreadersecret01234567", auth.StatusActive, auth.ScopeRead)

	if rec := do(t, srv, http.MethodGet, "/api/v1/activity", readTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("read-key auf /activity = %d, want 403", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/activity", adminToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin auf /activity = %d, want 200", rec.Code)
	}
}

func TestActivity_PresenceAndCounts(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", PresenceWindow: time.Minute})

	// Eine lesende Anfrage (info) unter dem Admin-Key.
	if rec := do(t, srv, http.MethodGet, "/api/v1/info", adminToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("info status = %d", rec.Code)
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/activity", adminToken, "")
	ar := decodeActivity(t, rec.Body.String())
	var found bool
	for _, k := range ar.Keys {
		if k.KID != testAdminKID {
			continue
		}
		found = true
		if !k.Online {
			t.Error("admin-key sollte nach Aktivität online sein")
		}
		if k.Reads < 1 {
			t.Errorf("erwarte Reads>=1 (info), habe %d", k.Reads)
		}
	}
	if !found {
		t.Fatalf("admin-kid %q nicht im Snapshot: %+v", testAdminKID, ar.Keys)
	}
	if ar.OnlineCount < 1 {
		t.Errorf("erwarte onlineCount>=1, habe %d", ar.OnlineCount)
	}
}

func TestActivity_DeniedCounted(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", PresenceWindow: time.Minute})
	readTok := seedKey(t, srv.store, "kid_reader2", "readersecretreadersecret76543210", auth.StatusActive, auth.ScopeRead)

	// read-key versucht eine admin-Route → 403, wird als Denied verbucht.
	if rec := do(t, srv, http.MethodGet, "/api/v1/activity", readTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	rec := do(t, srv, http.MethodGet, "/api/v1/activity", adminToken, "")
	ar := decodeActivity(t, rec.Body.String())
	for _, k := range ar.Keys {
		if k.KID == "kid_reader2" {
			if k.Denied < 1 {
				t.Errorf("erwarte Denied>=1 für read-key, habe %d", k.Denied)
			}
			if k.Online {
				t.Error("rein abgelehnter read-key sollte nicht online sein")
			}
			return
		}
	}
	t.Fatalf("read-key nicht im Snapshot: %+v", ar.Keys)
}

func TestWriteEvents_ReservedNamespaceRejected(t *testing.T) {
	srv := newTestServer(t)

	reserved := `{"events":[{"source":"x","subject":"/_clio/foo","type":"t"}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, reserved); rec.Code != http.StatusForbidden {
		t.Fatalf("client-write auf /_clio = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	ok := `{"events":[{"source":"x","subject":"/ok","type":"t"}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, ok); rec.Code != http.StatusOK {
		t.Fatalf("normaler write = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthEvents_DisabledByDefault(t *testing.T) {
	// Default-Config: AuthEvents aus → kein einziges /_clio-Event, auch nach
	// authentifizierten Anfragen (strikte Rückwärtskompatibilität).
	srv := newTestServer(t)

	for i := 0; i < 3; i++ {
		do(t, srv, http.MethodGet, "/api/v1/info", adminToken, "")
	}
	do(t, srv, http.MethodPost, "/api/v1/keys", adminToken, `{"name":"x","scopes":["read"]}`)

	if types := readEventTypes(t, srv, "/_clio", true); len(types) != 0 {
		t.Fatalf("erwarte 0 /_clio-Events bei AuthEvents=aus, habe %v", types)
	}
}

func TestAuthEvents_Enabled(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", PresenceWindow: time.Minute, AuthEvents: true})

	// Erste authentifizierte Anfrage → session-started für den Admin-Key.
	do(t, srv, http.MethodGet, "/api/v1/info", adminToken, "")
	sessTypes := readEventTypes(t, srv, "/_clio/auth/sessions/"+testAdminKID, true)
	if !contains(sessTypes, typeSessionStarted) {
		t.Fatalf("erwarte %s unter sessions/%s, habe %v", typeSessionStarted, testAdminKID, sessTypes)
	}

	// Key anlegen → key-created-Event.
	rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken, `{"name":"neu","scopes":["read"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("key anlegen = %d, want 201", rec.Code)
	}
	keyTypes := readEventTypes(t, srv, "/_clio/auth/keys", true)
	if !contains(keyTypes, typeKeyCreated) {
		t.Fatalf("erwarte %s unter keys, habe %v", typeKeyCreated, keyTypes)
	}

	// Kein Geheimnis im Event-Payload.
	body := `{"subject":"/_clio/auth","recursive":true}`
	if r := do(t, srv, http.MethodPost, "/api/v1/read-events", adminToken, body); strings.Contains(r.Body.String(), "secretHash") || strings.Contains(r.Body.String(), "adminsecret") {
		t.Error("auth-events dürfen kein Geheimnis enthalten")
	}
}

func TestMetrics_ExposesAuthAndPresence(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", PresenceWindow: time.Minute})
	do(t, srv, http.MethodGet, "/api/v1/info", adminToken, "")

	rec := do(t, srv, http.MethodGet, "/metrics", "", "")
	out := rec.Body.String()
	if !strings.Contains(out, "clio_auth_decisions_total{scope=\"read\",decision=\"allow\"}") {
		t.Errorf("clio_auth_decisions_total fehlt:\n%s", out)
	}
	if !strings.Contains(out, "clio_online_keys") {
		t.Errorf("clio_online_keys fehlt:\n%s", out)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
