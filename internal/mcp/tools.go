package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// tools ist die von tools/list gemeldete Fläche. Jedes Tool bildet auf genau
// einen clio-API-Aufruf ab; die Argumente spiegeln die bestehenden Request-
// Verträge (ADR-042).
var tools = []toolSpec{
	{
		Name:        "ping",
		Description: "Erreichbarkeitsprüfung (Liveness) der clio-Instanz. Ohne Auth.",
		InputSchema: obj(map[string]any{}),
	},
	{
		Name:        "info",
		Description: "Laufzeit-Infos der clio-Instanz: Version, Uptime, Event-Gesamtzahl, Sync-Modus und DB-Speicherbelegung. Verlangt read.",
		InputSchema: obj(map[string]any{}),
	},
	{
		Name:        "event_stats",
		Description: "Aggregierte Eventmengen über die Zeit (für das Dashboard). Verlangt read.",
		InputSchema: obj(map[string]any{
			"by": str("Optionale Gruppierung; \"source\" gruppiert nach CloudEvents-source."),
		}),
	},
	{
		Name:        "read_events",
		Description: "Events eines Subjects (oder Teilbaums) lesen. Liefert NDJSON, älteste zuerst. Verlangt read auf das Subject.",
		InputSchema: obj(map[string]any{
			"subject":    str("Subject-Pfad (mit \"/\" beginnend), z. B. \"/orders/42\". \"/\" = Wurzel."),
			"recursive":  boolp("Auch Events unterhalb des Subjects (Teilbaum) einbeziehen."),
			"lowerBound": str("Untere Event-ID-Schranke (inklusive)."),
			"upperBound": str("Obere Event-ID-Schranke (inklusive)."),
			"types":      arr("string", "Nur diese Event-Typen zurückgeben."),
			"limit":      intp("Maximale Trefferzahl; 0 = Server-Default (10000)."),
		}, "subject"),
	},
	{
		Name:        "run_query",
		Description: "Events per CEL-Prädikat filtern und projizieren. Verlangt read auf den Scope. Beispiel where: 'event.type == \"order.placed\" && event.data.amount > 100'.",
		InputSchema: obj(map[string]any{
			"subject":    str("Scope-Subject (mit \"/\" beginnend). \"/\" = alle."),
			"recursive":  boolp("Teilbaum unter subject einbeziehen."),
			"where":      str("CEL-Prädikat über 'event'; leer = alle im Scope."),
			"lowerBound": str("Untere Event-ID-Schranke (inklusive)."),
			"upperBound": str("Obere Event-ID-Schranke (inklusive)."),
			"limit":      intp("Maximale Trefferzahl; 0 = Server-Default."),
			"select":     arr("string", "Feldpfade zur Projektion; leer = ganzes Event."),
			"order":      str("\"asc\" (Default, älteste zuerst) oder \"desc\"."),
		}, "subject"),
	},
	{
		Name:        "write_events",
		Description: "Ein oder mehrere Events atomar anhängen (append-only). Optionale Preconditions für Optimistic Concurrency. Verlangt write auf jedes Subject.",
		InputSchema: obj(map[string]any{
			"events": map[string]any{
				"type":        "array",
				"description": "Event-Candidates. Jeder: {source, subject (mit \"/\"), type, data?}. ID/Time/Hash setzt der Server.",
				"items": obj(map[string]any{
					"source":  str("CloudEvents-source (Pflicht), z. B. \"/checkout-service\"."),
					"subject": str("Subject-Pfad, mit \"/\" beginnend (Pflicht)."),
					"type":    str("Event-Typ (Pflicht), z. B. \"order.placed\"."),
					"data":    map[string]any{"description": "Beliebiges JSON-Payload (optional)."},
				}, "source", "subject", "type"),
			},
			"preconditions": arr("object", "Optimistic-Concurrency-Bedingungen, z. B. {type, payload:{subject, eventId?, recursive?}}."),
		}, "events"),
	},
	{
		Name:        "read_subjects",
		Description: "Bekannte Subjects auflisten (NDJSON), optional unter einem Prefix. Verlangt read auf den Teilbaum.",
		InputSchema: obj(map[string]any{
			"prefix": str("Nur Subjects unter diesem Prefix (mit \"/\"); leer = ganzer Baum."),
			"tree":   boolp("Als hierarchischen Baum statt flacher NDJSON-Liste liefern."),
		}),
	},
	{
		Name:        "read_event_types",
		Description: "Alle bekannten Event-Typen auflisten (NDJSON). Verlangt read.",
		InputSchema: obj(map[string]any{}),
	},
	{
		Name:        "get_state",
		Description: "Gefaltete Zustandssicht eines Subjects: die data-Payloads werden per Last-Write-Wins (bzw. registrierter Reduce-Spec) zu einem aktuellen Zustand verdichtet (ADR-039). Single-Subject, nicht rekursiv. Verlangt read auf das Subject.",
		InputSchema: obj(map[string]any{
			"subject": str("Subject-Pfad (mit \"/\" beginnend), z. B. \"/orders/42\"."),
			"at":      str("Optionale obere Event-ID-Schranke: Zustand \"wie bei\" dieser ID."),
			"types":   arr("string", "Nur diese Event-Typen in die Faltung einbeziehen."),
		}, "subject"),
	},
	{
		Name:        "register_event_schema",
		Description: "Ein JSON-Schema für einen Event-Typ registrieren (ADR-014). Künftige Writes dieses Typs werden dagegen validiert. Verlangt globalen write.",
		InputSchema: obj(map[string]any{
			"type":   str("Event-Typ, für den das Schema gilt."),
			"schema": map[string]any{"description": "Das JSON-Schema als JSON-Objekt."},
		}, "type", "schema"),
	},
	{
		Name:        "read_event_schema",
		Description: "Das registrierte JSON-Schema eines Event-Typs lesen. Verlangt read.",
		InputSchema: obj(map[string]any{
			"type": str("Event-Typ, dessen Schema gelesen wird."),
		}, "type"),
	},
	{
		Name:        "register_reduce_spec",
		Description: "Eine deklarative Feld-Reduktionsstrategie für einen Subject-Prefix registrieren (ADR-041). Steuert, wie get_state Felder faltet. Verlangt globalen write.",
		InputSchema: obj(map[string]any{
			"prefix": str("Subject-Prefix (mit \"/\"), für den die Spec gilt."),
			"spec":   map[string]any{"description": "Die Reduce-Spec als JSON-Objekt: {default?, fields?}."},
		}, "prefix", "spec"),
	},
	{
		Name:        "read_reduce_spec",
		Description: "Reduce-Specs lesen: mit prefix die exakte Spec, mit subject die wirksame (längster Prefix), ohne Parameter alle (NDJSON). Verlangt read.",
		InputSchema: obj(map[string]any{
			"prefix":  str("Exakte Spec dieses Prefix."),
			"subject": str("Wirksame Spec für dieses Subject (längster passender Prefix)."),
		}),
	},
	{
		Name:        "delete_reduce_spec",
		Description: "Die Reduce-Spec eines Prefix entfernen. Verlangt globalen write.",
		InputSchema: obj(map[string]any{
			"prefix": str("Subject-Prefix (mit \"/\"), dessen Spec gelöscht wird."),
		}, "prefix"),
	},
}

// toolHandler ist die Signatur eines Tool-Handlers.
type toolHandler func(s *Server, ctx context.Context, raw json.RawMessage) any

// toolHandlers bildet Tool-Namen auf ihre Handler ab.
var toolHandlers = map[string]toolHandler{
	"ping":                  (*Server).toolPing,
	"info":                  (*Server).toolInfo,
	"event_stats":           (*Server).toolEventStats,
	"read_events":           (*Server).toolReadEvents,
	"run_query":             (*Server).toolRunQuery,
	"write_events":          (*Server).toolWriteEvents,
	"read_subjects":         (*Server).toolReadSubjects,
	"read_event_types":      (*Server).toolReadEventTypes,
	"get_state":             (*Server).toolGetState,
	"register_event_schema": (*Server).toolRegisterEventSchema,
	"read_event_schema":     (*Server).toolReadEventSchema,
	"register_reduce_spec":  (*Server).toolRegisterReduceSpec,
	"read_reduce_spec":      (*Server).toolReadReduceSpec,
	"delete_reduce_spec":    (*Server).toolDeleteReduceSpec,
}

// forward setzt einen Aufruf ab und übersetzt das Ergebnis in ein MCP-Result:
// Transportfehler und clio-4xx/5xx werden als toolError (isError) gemeldet, der
// Erfolgs-Body unverändert durchgereicht.
func (s *Server) forward(ctx context.Context, method, path string, query url.Values, body io.Reader) any {
	resp, err := s.client.call(ctx, method, path, query, body)
	if err != nil {
		return toolError("clio nicht erreichbar: " + err.Error())
	}
	if !resp.ok() {
		return toolError("clio antwortete mit HTTP " + itoa(resp.status) + ": " + string(resp.body))
	}
	return toolResult(resp.body)
}

// bodyOf liefert einen Reader über die rohen Argumente (oder nil bei leer). Für
// POST-Tools, deren Request-Body den Argumenten entspricht, ist das ein direkter
// Durchgriff — die Validierung übernimmt clio.
func bodyOf(raw json.RawMessage) io.Reader {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return bytes.NewReader(raw)
}

func (s *Server) toolPing(ctx context.Context, _ json.RawMessage) any {
	return s.forward(ctx, "GET", "/api/v1/ping", nil, nil)
}

func (s *Server) toolInfo(ctx context.Context, _ json.RawMessage) any {
	return s.forward(ctx, "GET", "/api/v1/info", nil, nil)
}

func (s *Server) toolEventStats(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		By string `json:"by"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	var q url.Values
	if a.By != "" {
		q = url.Values{"by": {a.By}}
	}
	return s.forward(ctx, "GET", "/api/v1/event-stats", q, nil)
}

func (s *Server) toolReadEvents(ctx context.Context, raw json.RawMessage) any {
	return s.forward(ctx, "POST", "/api/v1/read-events", nil, bodyOf(raw))
}

func (s *Server) toolRunQuery(ctx context.Context, raw json.RawMessage) any {
	return s.forward(ctx, "POST", "/api/v1/run-query", nil, bodyOf(raw))
}

func (s *Server) toolWriteEvents(ctx context.Context, raw json.RawMessage) any {
	return s.forward(ctx, "POST", "/api/v1/write-events", nil, bodyOf(raw))
}

func (s *Server) toolReadSubjects(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		Prefix string `json:"prefix"`
		Tree   bool   `json:"tree"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	q := url.Values{}
	if a.Prefix != "" {
		q.Set("prefix", a.Prefix)
	}
	if a.Tree {
		q.Set("tree", "true")
	}
	return s.forward(ctx, "GET", "/api/v1/read-subjects", q, nil)
}

func (s *Server) toolReadEventTypes(ctx context.Context, _ json.RawMessage) any {
	return s.forward(ctx, "GET", "/api/v1/read-event-types", nil, nil)
}

func (s *Server) toolGetState(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		Subject string   `json:"subject"`
		At      string   `json:"at"`
		Types   []string `json:"types"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	if strings.TrimSpace(a.Subject) == "" {
		return toolError("missing required argument: subject")
	}
	q := url.Values{}
	if a.At != "" {
		q.Set("at", a.At)
	}
	for _, t := range a.Types {
		q.Add("type", t)
	}
	return s.forward(ctx, "GET", "/api/v1/state/"+escapeSubject(a.Subject), q, nil)
}

func (s *Server) toolRegisterEventSchema(ctx context.Context, raw json.RawMessage) any {
	return s.forward(ctx, "POST", "/api/v1/register-event-schema", nil, bodyOf(raw))
}

func (s *Server) toolReadEventSchema(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		Type string `json:"type"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	if strings.TrimSpace(a.Type) == "" {
		return toolError("missing required argument: type")
	}
	return s.forward(ctx, "GET", "/api/v1/read-event-schema", url.Values{"type": {a.Type}}, nil)
}

func (s *Server) toolRegisterReduceSpec(ctx context.Context, raw json.RawMessage) any {
	return s.forward(ctx, "POST", "/api/v1/register-reduce-spec", nil, bodyOf(raw))
}

func (s *Server) toolReadReduceSpec(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		Prefix  string `json:"prefix"`
		Subject string `json:"subject"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	q := url.Values{}
	if a.Subject != "" {
		q.Set("subject", a.Subject)
	} else if a.Prefix != "" {
		q.Set("prefix", a.Prefix)
	}
	return s.forward(ctx, "GET", "/api/v1/read-reduce-spec", q, nil)
}

func (s *Server) toolDeleteReduceSpec(ctx context.Context, raw json.RawMessage) any {
	var a struct {
		Prefix string `json:"prefix"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return toolError(err.Error())
	}
	if strings.TrimSpace(a.Prefix) == "" {
		return toolError("missing required argument: prefix")
	}
	return s.forward(ctx, "DELETE", "/api/v1/reduce-spec", url.Values{"prefix": {a.Prefix}}, nil)
}

// unmarshalArgs dekodiert die Tool-Argumente; leere Argumente sind zulässig.
func unmarshalArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errInvalidArgs(err)
	}
	return nil
}

func errInvalidArgs(err error) error { return &argError{err} }

type argError struct{ err error }

func (e *argError) Error() string { return "invalid arguments: " + e.err.Error() }

// escapeSubject baut den Pfad-Rest der state-Route: führendes "/" entfernen und
// jedes Segment url-escapen, damit Sonderzeichen im Subject korrekt übertragen
// werden.
func escapeSubject(subject string) string {
	trimmed := strings.TrimPrefix(subject, "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// itoa ist ein kleiner, allokationsarmer Int→String-Helfer für Fehlermeldungen.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
