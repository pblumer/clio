package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// ndjsonContentType ist der Content-Type für Newline-Delimited JSON.
const ndjsonContentType = "application/x-ndjson"

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("ungültiger request-body: " + err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// problemContentType ist der Media-Type für RFC-7807-Fehler.
const problemContentType = "application/problem+json"

// problemDetails ist ein strukturierter Fehler-Body nach RFC 7807
// (application/problem+json). `type` bleibt generisch ("about:blank"); `title`
// ist der HTTP-Statustext, `detail` die konkrete Meldung.
type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// writeError schreibt einen Fehler als application/problem+json (RFC 7807) —
// ein konfliktfreier Quick Win Richtung Swiss API Guidelines (ADR-019). Die
// Signatur bleibt unverändert, damit alle Aufrufstellen profitieren.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetails{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}

// writeNDJSON schreibt eine Werteliste als Newline-Delimited JSON (ein JSON-
// Objekt pro Zeile). Generisch, damit sowohl Events als auch projizierte
// Objekte ausgegeben werden können.
func writeNDJSON[T any](w http.ResponseWriter, logger *slog.Logger, items []T) {
	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, ev := range items {
		if err := enc.Encode(ev); err != nil {
			// Header sind bereits gesendet; nur noch loggen.
			logger.Error("ndjson schreiben fehlgeschlagen", "err", err)
			return
		}
	}
}
