package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ServeHTTP implementiert den MCP-HTTP-Transport („Streamable HTTP"): ein POST
// trägt genau eine JSON-RPC-Nachricht, die Antwort ist die zugehörige
// JSON-RPC-Antwort (Content-Type application/json) bzw. 202 Accepted ohne Body,
// wenn die Nachricht eine Notification war. Der eingebettete Mount (/mcp) und
// das eigenständige Binary (-http) nutzen denselben Handler.
//
// Andere Methoden beantwortet der Handler mit 405 und Allow: POST. Das ist kein
// Mangel, sondern das von der Spec vorgesehene Signal: ein Client, der per GET
// einen server-initiierten SSE-Strom öffnen (oder per DELETE eine Session
// beenden) möchte, erkennt daran, dass dieser Server rein request/response
// arbeitet, und bleibt bei POST. Ein 404 an dieser Stelle würde denselben Client
// dagegen die Verbindung als gescheitert melden lassen.
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
		// Notification/Response: verarbeitet, kein Body. Die Streamable-HTTP-
		// Spec verlangt hier ausdrücklich 202 Accepted — Clients (u. a. das
		// MCP-TypeScript-SDK) verzweigen genau auf diesen Code, bevor sie den
		// Body zu lesen versuchen. Ein 204 ist zwar semantisch ähnlich, fällt
		// bei strikten Clients aber in den Zweig „unerwarteter Content-Type".
		w.WriteHeader(http.StatusAccepted)
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
