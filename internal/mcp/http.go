package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ServeHTTP implementiert den MCP-HTTP-Transport: ein POST trägt genau eine
// JSON-RPC-Nachricht, die Antwort ist die zugehörige JSON-RPC-Antwort. Der
// eingebettete Mount (POST /mcp) und das eigenständige Binary (-http) nutzen
// denselben Handler.
//
// Der MCP-Layer selbst authentifiziert nicht: das Authorization-Bearer der
// eingehenden Anfrage wird als Identität des Aufrufers an clios API
// durchgereicht (ADR-042). Jeder Tool-Call unterliegt damit clios Scopes
// (ADR-025/033) — der eigentliche Zugriffsschutz. Ohne Bearer greift das feste
// Fallback-Token des Clients (eigenständiges Binary mit -token).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "nur POST", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageBytes))
	if err != nil {
		writeRPCError(w, codeParseError, "konnte Anfrage nicht lesen: "+err.Error())
		return
	}

	// Identität des Aufrufers an clios API weiterreichen.
	ctx := WithToken(r.Context(), bearerToken(r.Header.Get("Authorization")))

	resp, ok := s.handleMessage(ctx, body)
	if !ok {
		// Notification: verarbeitet, keine Antwort.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// bearerToken extrahiert das `kid.secret` aus einem "Bearer …"-Header (ohne
// Präfix); leer, wenn keiner vorliegt.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) >= len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}

// writeRPCError schreibt eine eigenständige JSON-RPC-Fehlerantwort (id=null).
func writeRPCError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(errorResponse(nil, code, msg))
}
