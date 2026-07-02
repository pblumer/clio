package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// Client ist der dünne HTTP-Adapter auf clios öffentliche API. Jeder Tool-Call
// wird in genau einen HTTP-Request gegen baseURL übersetzt und über rt
// abgesetzt. So lebt die Geschäftslogik (Auth-Scopes, Preconditions, Fold,
// Schema-Validierung) unverändert in den bestehenden internal/httpapi-Handlern
// und wird nicht dupliziert (ADR-042).
//
// Der zu verwendende API-Key (`kid.secret`, ADR-025) kommt entweder aus dem
// Kontext (pro Anfrage weitergereicht, eingebetteter Modus) oder — als Fallback —
// aus dem festen Token des Clients (eigenständiges Binary).
type Client struct {
	baseURL string
	token   string
	rt      http.RoundTripper
}

// NewClient erstellt einen Client gegen baseURL (z. B. "http://127.0.0.1:3000").
// token ist das feste Fallback-Bearer (`kid.secret`); ist rt nil, wird
// http.DefaultTransport verwendet.
func NewClient(baseURL, token string, rt http.RoundTripper) *Client {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, rt: rt}
}

// tokenCtxKey ist der Kontext-Schlüssel für das pro Anfrage weitergereichte
// Bearer-Token (eingebetteter POST /mcp-Modus).
type tokenCtxKey struct{}

// WithToken hinterlegt ein Bearer-Token (`kid.secret`, ohne "Bearer "-Präfix) im
// Kontext. Der HTTP-Transport nutzt es, um die Identität des MCP-Aufrufers an
// clios API durchzureichen.
func WithToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, tokenCtxKey{}, token)
}

// tokenFromContext liest das weitergereichte Token; leer, wenn keines gesetzt ist.
func tokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tokenCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// tokenFor wählt das wirksame Bearer-Token: Kontext (weitergereicht) hat Vorrang
// vor dem festen Client-Token.
func (c *Client) tokenFor(ctx context.Context) string {
	if t := tokenFromContext(ctx); t != "" {
		return t
	}
	return c.token
}

// response bündelt das Ergebnis eines HTTP-Aufrufs an clios API.
type response struct {
	status int
	body   []byte
}

// ok meldet, ob der Statuscode einen Erfolg (2xx) darstellt.
func (r response) ok() bool { return r.status >= 200 && r.status < 300 }

// call setzt einen HTTP-Request gegen clios API ab und liest die vollständige
// Antwort. Streamende Antworten (NDJSON) werden dabei komplett gepuffert — die
// Tool-Fläche liefert bewusst keine offenen Streams (ADR-042).
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body io.Reader) (response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return response{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.tokenFor(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Antwortgröße begrenzen — dieselbe Obergrenze wie für eingehende Nachrichten.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, body: data}, nil
}

// HandlerTransport ist ein http.RoundTripper, der Requests in-process an einen
// http.Handler (clios eigenen Mux) verteilt — ohne echten Netzwerk-Hop. Der
// eingebettete POST /mcp-Mount nutzt ihn, um dieselbe API-Fläche samt Auth ohne
// Loopback-Socket wiederzuverwenden (ADR-042).
func HandlerTransport(h http.Handler) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Result(), nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
