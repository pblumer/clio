package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer erstellt einen MCP-Server, dessen Client auf ein Fake-clio zeigt,
// das jede Anfrage mit status/body beantwortet und (falls capture != nil) den
// letzten Request festhält.
func newTestServer(t *testing.T, status int, body string, capture *capturedReq) (*Server, func()) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			buf, _ := io.ReadAll(r.Body)
			*capture = capturedReq{
				method: r.Method,
				path:   r.URL.Path,
				query:  r.URL.RawQuery,
				auth:   r.Header.Get("Authorization"),
				body:   buf,
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	srv := NewServer(NewClient(fake.URL, "kid.secret", nil), WithVersion("test"))
	return srv, fake.Close
}

type capturedReq struct {
	method string
	path   string
	query  string
	auth   string
	body   []byte
}

func TestInitialize(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()

	res, rerr := srv.dispatch(context.Background(), "initialize", json.RawMessage(`{"protocolVersion":"2025-06-18"}`))
	if rerr != nil {
		t.Fatalf("unerwarteter Fehler: %+v", rerr)
	}
	m := res.(map[string]any)
	if m["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, erwartet Echo der Client-Version", m["protocolVersion"])
	}
	info := m["serverInfo"].(map[string]any)
	if info["name"] != serverName || info["version"] != "test" {
		t.Errorf("serverInfo = %v", info)
	}
}

func TestInitializeDefaultVersion(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	res, _ := srv.dispatch(context.Background(), "initialize", nil)
	if got := res.(map[string]any)["protocolVersion"]; got != defaultProtocolVersion {
		t.Errorf("protocolVersion = %v, erwartet Default %v", got, defaultProtocolVersion)
	}
}

func TestToolsListEnthältAlleTools(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	res, rerr := srv.dispatch(context.Background(), "tools/list", nil)
	if rerr != nil {
		t.Fatal(rerr)
	}
	list := res.(map[string]any)["tools"].([]toolSpec)
	if len(list) != len(toolHandlers) {
		t.Errorf("tools/list meldet %d Tools, toolHandlers hat %d — Fläche divergiert", len(list), len(toolHandlers))
	}
	for _, spec := range list {
		if _, ok := toolHandlers[spec.Name]; !ok {
			t.Errorf("tool %q in tools/list hat keinen Handler", spec.Name)
		}
	}
}

func TestDispatchUnbekannteMethode(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	_, rerr := srv.dispatch(context.Background(), "does/not/exist", nil)
	if rerr == nil || rerr.Code != codeMethodNotFound {
		t.Errorf("erwartet MethodNotFound, bekam %+v", rerr)
	}
}

func TestServeStdioRoundtripUndNotification(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, `{"status":"ok"}`, nil)
	defer closeFn()

	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}` + "\n",
	)
	var out strings.Builder
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Genau zwei Antworten (initialize + tools/call); die Notification erzeugt keine.
	if len(lines) != 2 {
		t.Fatalf("erwartet 2 Antwortzeilen, bekam %d: %q", len(lines), out.String())
	}
	var pingResp rpcResponse
	if err := json.Unmarshal([]byte(lines[1]), &pingResp); err != nil {
		t.Fatal(err)
	}
	if pingResp.Error != nil {
		t.Errorf("ping-Antwort hat Fehler: %+v", pingResp.Error)
	}
}

func TestServeStdioParseError(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	var out strings.Builder
	if err := srv.Serve(context.Background(), strings.NewReader("{kaputt\n"), &out); err != nil {
		t.Fatal(err)
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Errorf("erwartet ParseError, bekam %+v", resp.Error)
	}
}
