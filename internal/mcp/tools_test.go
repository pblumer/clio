package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// callTool ruft ein Tool direkt über handleToolsCall auf und liefert das
// MCP-Ergebnis als map.
func callTool(t *testing.T, srv *Server, name string, args any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(raw)})
	res, rerr := srv.handleToolsCall(context.Background(), params)
	if rerr != nil {
		t.Fatalf("handleToolsCall(%s) Protokollfehler: %+v", name, rerr)
	}
	return res.(map[string]any)
}

// text extrahiert den Text des ersten Content-Blocks.
func text(m map[string]any) string {
	content := m["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func TestToolTranslation(t *testing.T) {
	cases := []struct {
		name      string
		args      any
		wantMeth  string
		wantPath  string
		wantQuery string          // erwartete RawQuery (Reihenfolge wie url.Values.Encode)
		wantBody  json.RawMessage // erwarteter durchgereichter Body (POST); nil = keiner
	}{
		{"ping", map[string]any{}, "GET", "/api/v1/ping", "", nil},
		{"info", map[string]any{}, "GET", "/api/v1/info", "", nil},
		{"event_stats", map[string]any{"by": "source"}, "GET", "/api/v1/event-stats", "by=source", nil},
		{"read_event_types", map[string]any{}, "GET", "/api/v1/read-event-types", "", nil},
		{"read_subjects", map[string]any{"prefix": "/orders", "tree": true}, "GET", "/api/v1/read-subjects", "prefix=%2Forders&tree=true", nil},
		{"read_event_schema", map[string]any{"type": "order.placed"}, "GET", "/api/v1/read-event-schema", "type=order.placed", nil},
		{"read_reduce_spec", map[string]any{"subject": "/orders/1"}, "GET", "/api/v1/read-reduce-spec", "subject=%2Forders%2F1", nil},
		{"delete_reduce_spec", map[string]any{"prefix": "/orders"}, "DELETE", "/api/v1/reduce-spec", "prefix=%2Forders", nil},
		{"get_state", map[string]any{"subject": "/orders/1", "at": "42", "types": []string{"a", "b"}}, "GET", "/api/v1/state/orders/1", "at=42&type=a&type=b", nil},
		{
			"read_events",
			map[string]any{"subject": "/orders", "recursive": true, "limit": 5},
			"POST", "/api/v1/read-events", "",
			json.RawMessage(`{"subject":"/orders","recursive":true,"limit":5}`),
		},
		{
			"run_query",
			map[string]any{"subject": "/", "where": "event.type == 'x'"},
			"POST", "/api/v1/run-query", "",
			json.RawMessage(`{"subject":"/","where":"event.type == 'x'"}`),
		},
		{
			"write_events",
			map[string]any{"events": []any{map[string]any{"source": "/s", "subject": "/o/1", "type": "t"}}},
			"POST", "/api/v1/write-events", "",
			json.RawMessage(`{"events":[{"source":"/s","subject":"/o/1","type":"t"}]}`),
		},
		{
			"register_event_schema",
			map[string]any{"type": "t", "schema": map[string]any{"type": "object"}},
			"POST", "/api/v1/register-event-schema", "",
			json.RawMessage(`{"type":"t","schema":{"type":"object"}}`),
		},
		{
			"register_reduce_spec",
			map[string]any{"prefix": "/o", "spec": map[string]any{"default": "lww"}},
			"POST", "/api/v1/register-reduce-spec", "",
			json.RawMessage(`{"prefix":"/o","spec":{"default":"lww"}}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cap capturedReq
			srv, closeFn := newTestServer(t, http.StatusOK, `{"ok":true}`, &cap)
			defer closeFn()

			out := callTool(t, srv, tc.name, tc.args)
			if isErr, _ := out["isError"].(bool); isErr {
				t.Fatalf("unerwarteter Tool-Fehler: %s", text(out))
			}
			if cap.method != tc.wantMeth || cap.path != tc.wantPath {
				t.Errorf("%s → %s %s, erwartet %s %s", tc.name, cap.method, cap.path, tc.wantMeth, tc.wantPath)
			}
			if cap.query != tc.wantQuery {
				t.Errorf("%s Query = %q, erwartet %q", tc.name, cap.query, tc.wantQuery)
			}
			if tc.wantBody != nil {
				if !jsonEqual(t, cap.body, tc.wantBody) {
					t.Errorf("%s Body = %s, erwartet %s", tc.name, cap.body, tc.wantBody)
				}
			}
			// Der Erfolgs-Body wird unverändert durchgereicht.
			if got := text(out); got != `{"ok":true}` {
				t.Errorf("%s Ergebnis-Text = %q, erwartet Durchgriff", tc.name, got)
			}
			// Das feste Client-Token wird als Bearer gesetzt.
			if cap.auth != "Bearer kid.secret" {
				t.Errorf("%s Authorization = %q, erwartet Fallback-Token", tc.name, cap.auth)
			}
		})
	}
}

func TestToolFehlerWirdIsError(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusConflict, `{"detail":"precondition failed"}`, nil)
	defer closeFn()
	out := callTool(t, srv, "write_events", map[string]any{"events": []any{}})
	if isErr, _ := out["isError"].(bool); !isErr {
		t.Fatalf("erwartet isError bei HTTP 409, bekam %v", out)
	}
	if !strings.Contains(text(out), "409") || !strings.Contains(text(out), "precondition failed") {
		t.Errorf("Fehlertext trägt Status/Body nicht: %q", text(out))
	}
}

func TestToolFehlendesPflichtargument(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	out := callTool(t, srv, "get_state", map[string]any{})
	if isErr, _ := out["isError"].(bool); !isErr {
		t.Fatalf("erwartet isError bei fehlendem subject")
	}
	if !strings.Contains(text(out), "subject") {
		t.Errorf("Fehlertext nennt subject nicht: %q", text(out))
	}
}

func TestUnbekanntesTool(t *testing.T) {
	srv, closeFn := newTestServer(t, http.StatusOK, "", nil)
	defer closeFn()
	params, _ := json.Marshal(map[string]any{"name": "gibts_nicht", "arguments": map[string]any{}})
	res, rerr := srv.handleToolsCall(context.Background(), params)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if isErr, _ := res.(map[string]any)["isError"].(bool); !isErr {
		t.Errorf("unbekanntes Tool sollte isError liefern")
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var xa, xb any
	if err := json.Unmarshal(a, &xa); err != nil {
		t.Fatalf("Body a kein JSON: %s (%v)", a, err)
	}
	if err := json.Unmarshal(b, &xb); err != nil {
		t.Fatalf("Body b kein JSON: %s (%v)", b, err)
	}
	na, _ := json.Marshal(xa)
	nb, _ := json.Marshal(xb)
	return string(na) == string(nb)
}
