package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
)

// JSON-RPC-2.0-Fehlercodes des MCP-stdio-Transports.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// maxMessageBytes deckelt eine einzelne JSON-RPC-Nachricht (eine Zeile), damit
// ein fehlerhafter oder feindlicher Strom den Speicher nicht erschöpft. Eingaben
// sind grundsätzlich nicht vertrauenswürdig.
const maxMessageBytes = 8 << 20 // 8 MiB

// rpcRequest ist eine eingehende JSON-RPC-2.0-Anfrage oder -Notification. Eine
// Notification trägt kein id-Member, was der Transport an einer leeren ID erkennt.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse ist eine ausgehende JSON-RPC-2.0-Antwort. Genau eines von Result
// oder Error ist gesetzt.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError ist ein JSON-RPC-2.0-Fehlerobjekt.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Serve fährt den MCP-stdio-Transport: es liest zeilengetrennte JSON-RPC-
// Nachrichten aus r und schreibt Antworten nach w, bis r EOF erreicht oder ctx
// abgebrochen wird. Jede Nachricht ist eine JSON-Zeile ohne eingebettete
// Zeilenumbrüche (Vorgabe des stdio-Transports); Notifications (Anfragen ohne id)
// werden verarbeitet, erzeugen aber keine Antwort.
//
// w erhält ausschließlich Protokoll-Nachrichten — Aufrufer müssen Diagnose-
// Logging von diesem Strom fernhalten (stderr verwenden), sonst scheitert der
// Client am Parsen.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		resp, ok := s.handleMessage(ctx, line)
		if !ok {
			continue // Notification: keine Antwort
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// handleMessage parst und verteilt eine JSON-RPC-Zeile. Der bool ist false, wenn
// die Nachricht eine Notification (ohne id) ist und daher keine Antwort ergibt.
func (s *Server) handleMessage(ctx context.Context, line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, codeParseError, "parse error: "+err.Error()), true
	}

	result, rerr := s.dispatch(ctx, req.Method, req.Params)

	// Eine Anfrage ohne id ist eine Notification: nur für den Effekt verarbeiten,
	// nichts antworten.
	if len(req.ID) == 0 {
		return rpcResponse{}, false
	}
	if rerr != nil {
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rerr}, true
	}
	return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
}

// dispatch leitet eine JSON-RPC-Methode an ihren Handler. Es liefert entweder ein
// Ergebnis oder einen Protokoll-Fehler; Domänenfehler innerhalb eines Tool-Calls
// werden im Ergebnis mit isError gemeldet, nicht als JSON-RPC-Fehler, damit der
// Agent sie lesen kann.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "notifications/initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		return s.handleToolsCall(ctx, params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
	}
}

// errorResponse baut eine JSON-RPC-Fehlerantwort mit der gegebenen id (nil → null).
func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
