package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pblumer/clio/internal/config"
)

func newMCPServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerCfg(t, config.Config{Addr: ":0", MCPEnabled: true})
}

const initializeMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`

// TestMCPMountHandshake deckt den Einstieg eines Streamable-HTTP-Clients ab —
// inklusive des konfigurierten Trailing Slash, der bisher in ein 404 lief.
func TestMCPMountHandshake(t *testing.T) {
	srv := newMCPServer(t)

	for _, path := range []string{"/mcp", "/mcp/"} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, path, "", initializeMsg)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			var resp struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
					ServerInfo      struct {
						Name string `json:"name"`
					} `json:"serverInfo"`
				} `json:"result"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("antwort dekodieren: %v", err)
			}
			if resp.Result.ServerInfo.Name != "clio-mcp" {
				t.Errorf("serverInfo.name = %q, want clio-mcp", resp.Result.ServerInfo.Name)
			}
			if resp.Result.ProtocolVersion != "2025-06-18" {
				t.Errorf("protocolVersion = %q, want die des Clients", resp.Result.ProtocolVersion)
			}
		})
	}
}

// TestMCPNotificationAccepted: eine Notification wird mit 202 quittiert (Spec).
func TestMCPNotificationAccepted(t *testing.T) {
	rec := do(t, newMCPServer(t), http.MethodPost, "/mcp", "",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want leer", body)
	}
}

// TestMCPOptionaleMethoden: GET (SSE-Strom) und DELETE (Session beenden) sind
// optional. Der Client muss ein 405 mit Allow sehen — bei einem 404 bricht er
// die Verbindung ab.
func TestMCPOptionaleMethoden(t *testing.T) {
	srv := newMCPServer(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := do(t, srv, method, "/mcp", "", "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
				t.Errorf("Allow = %q, want POST", allow)
			}
		})
	}
}

// TestMCPAusgeschaltet: ohne CLIO_MCP existiert die Route nicht — dann aber als
// wohlgeformtes problem+json, nicht als Klartext des ServeMux.
func TestMCPAusgeschaltet(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodPost, "/mcp", "", initializeMsg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertProblemJSON(t, rec.Header().Get("Content-Type"), rec.Body.Bytes(), http.StatusNotFound)
}

// TestUnbekannterPfadAlsProblemJSON: unbekannte Pfade — u. a. die OAuth-Discovery,
// die MCP-Clients beim Verbinden abtasten — beantwortet clio als problem+json
// (ADR-019) statt mit dem Klartext „404 page not found" des ServeMux.
func TestUnbekannterPfadAlsProblemJSON(t *testing.T) {
	srv := newMCPServer(t)
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
		"/gibtesnicht",
	} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, srv, http.MethodGet, path, "", "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			assertProblemJSON(t, rec.Header().Get("Content-Type"), rec.Body.Bytes(), http.StatusNotFound)
		})
	}
}

// TestFalscheMethodeBleibt405 sichert ab, dass die problem+json-Ersetzung die
// Unterscheidung des ServeMux zwischen „Pfad unbekannt" (404) und „Methode
// falsch" (405) nicht einebnet.
func TestFalscheMethodeBleibt405(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodPost, "/api/v1/info", adminToken, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Error("Allow-Header fehlt")
	}
	assertProblemJSON(t, rec.Header().Get("Content-Type"), rec.Body.Bytes(), http.StatusMethodNotAllowed)
}

func assertProblemJSON(t *testing.T, contentType string, body []byte, wantStatus int) {
	t.Helper()
	if contentType != problemContentType {
		t.Errorf("Content-Type = %q, want %q", contentType, problemContentType)
	}
	var p problemDetails
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("body ist kein JSON (%v): %s", err, body)
	}
	if p.Status != wantStatus {
		t.Errorf("problem.status = %d, want %d", p.Status, wantStatus)
	}
	if p.Title != http.StatusText(wantStatus) {
		t.Errorf("problem.title = %q, want %q", p.Title, http.StatusText(wantStatus))
	}
	if p.Detail == "" {
		t.Error("problem.detail ist leer")
	}
}
