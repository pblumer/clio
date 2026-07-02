package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeHTTPNurPost(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp = %d, erwartet 405", rec.Code)
	}
}

func TestServeHTTPNotification(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", body))
	if rec.Code != http.StatusNoContent {
		t.Errorf("Notification = %d, erwartet 204", rec.Code)
	}
}

// TestInProcessTransportUndBearerWeiterreichung prüft den eingebetteten Modus:
// ein POST /mcp mit Bearer wird per HandlerTransport in-process an den Backend-
// Handler verteilt, und die Identität des Aufrufers erreicht die API.
func TestInProcessTransportUndBearerWeiterreichung(t *testing.T) {
	var gotAuth, gotPath string
	backend := http.NewServeMux()
	backend.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"name":"cliostore"}`))
	})

	srv := NewServer(NewClient("http://clio", "", HandlerTransport(backend)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"info"}}`))
	req.Header.Set("Authorization", "Bearer caller.secret")
	srv.ServeHTTP(rec, req)

	if gotPath != "/api/v1/info" {
		t.Errorf("Backend-Pfad = %q, erwartet /api/v1/info", gotPath)
	}
	if gotAuth != "Bearer caller.secret" {
		t.Errorf("weitergereichtes Bearer = %q, erwartet das des Aufrufers", gotAuth)
	}

	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	if txt := content[0].(map[string]any)["text"].(string); !strings.Contains(txt, "cliostore") {
		t.Errorf("Ergebnis reicht Backend-Antwort nicht durch: %q", txt)
	}
}

func TestBearerToken(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Bearer kid.secret", "kid.secret"},
		{"bearer kid.secret", "kid.secret"},
		{"Basic abc", ""},
		{"", ""},
	} {
		if got := bearerToken(tc.in); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, erwartet %q", tc.in, got, tc.want)
		}
	}
}
