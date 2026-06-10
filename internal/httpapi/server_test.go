package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/clio/internal/config"
)

func newTestServer() *Server {
	return New(config.Config{Addr: ":0", APIToken: "secret-token"}, nil)
}

func TestPing(t *testing.T) {
	srv := newTestServer()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/ping", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("antwort dekodieren: %v", err)
			}
			if body["status"] != "ok" {
				t.Fatalf("status feld = %q, want %q", body["status"], "ok")
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	srv := newTestServer()
	protected := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"gültiges token", "Bearer secret-token", http.StatusNoContent},
		{"falsches token", "Bearer wrong", http.StatusUnauthorized},
		{"kein header", "", http.StatusUnauthorized},
		{"ohne bearer prefix", "secret-token", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/write-events", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			protected(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
