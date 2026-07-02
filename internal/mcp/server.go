// Package mcp stellt clios Event-Store über das Model Context Protocol (MCP)
// als native Agent-Tools bereit. Es ist ein dünner Adapter, der ausschließlich
// über clios öffentliche HTTP-API reicht (nicht über internen Store-Zugriff),
// damit Geschäftslogik — Auth-Scopes (ADR-025/033), Preconditions, gefaltete
// Zustandssicht (ADR-039), Schema-Validierung — an einer Stelle bleibt und nicht
// dupliziert wird (ADR-042).
//
// Das Protokoll ist JSON-RPC 2.0 über den stdio- und den HTTP-Transport,
// implementiert allein mit der Standardbibliothek (kein MCP-SDK, ADR-001). Der
// Server läuft in zwei Modi mit identischer Tool-Fläche: als eigenständiges
// Binary (cmd/clio-mcp) gegen eine laufende clio-Instanz und eingebettet unter
// POST /mcp im cliostore selbst (In-Process-Transport, siehe HandlerTransport).
package mcp

import (
	"context"
	"encoding/json"
)

const (
	serverName             = "clio-mcp"
	defaultProtocolVersion = "2024-11-05"
)

// Server ist der MCP-Server: er hält den API-Client und die Server-Metadaten.
type Server struct {
	client  *Client
	version string
}

// Option konfiguriert optionale Server-Metadaten.
type Option func(*Server)

// WithVersion setzt die im initialize gemeldete Server-Version.
func WithVersion(v string) Option {
	return func(s *Server) {
		if v != "" {
			s.version = v
		}
	}
}

// NewServer erstellt einen MCP-Server, der Tool-Calls über client an clios API
// weiterreicht.
func NewServer(client *Client, opts ...Option) *Server {
	s := &Server{client: client, version: "dev"}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// handleInitialize beantwortet den MCP-Handshake: Protokollversion, Fähigkeiten
// (nur Tools) und Server-Info.
func (s *Server) handleInitialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	version := p.ProtocolVersion
	if version == "" {
		version = defaultProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": s.version},
	}, nil
}

// toolSpec beschreibt ein Tool für tools/list.
type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any   { return map[string]any{"type": "string", "description": desc} }
func boolp(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func intp(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func arr(item, desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": item}}
}

// handleToolsCall verteilt einen tools/call an den passenden Tool-Handler.
func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	h, ok := toolHandlers[p.Name]
	if !ok {
		return toolError("unknown tool: " + p.Name), nil
	}
	return h(s, ctx, p.Arguments), nil
}

// toolResult verpackt eine erfolgreiche clio-Antwort als MCP-Text-Content. Der
// Body wird unverändert durchgereicht (JSON oder NDJSON), damit der Agent die
// Antwort exakt so sieht, wie clios API sie liefert.
func toolResult(body []byte) any {
	text := string(body)
	if text == "" {
		text = "(leere Antwort)"
	}
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

// toolError verpackt einen Fehler als MCP-Ergebnis mit isError=true, sodass der
// Agent die Meldung lesen und darauf reagieren kann (statt eines abstrakten
// Protokollfehlers).
func toolError(msg string) any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
		"isError": true,
	}
}
