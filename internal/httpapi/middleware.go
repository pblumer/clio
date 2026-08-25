package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

// statusRecorder fängt den Status-Code (und reicht http.Flusher fürs Streaming
// durch), um Anfragen instrumentieren zu können.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gibt den umschlossenen ResponseWriter frei, damit http.ResponseController
// (net/http) an die darunterliegenden optionalen Interfaces gelangt — insbesondere
// SetWriteDeadline. Ohne dieses Unwrap liefert der Controller ErrNotSupported und
// die Deadline-Aufhebung der Streaming-Handler (observe-events, große read-/query-
// Scans, backup) läuft ins Leere: der Server-WriteTimeout würde diese bewusst
// langlaufenden bzw. unendlichen Ströme nach WriteTimeout hart kappen.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// instrument loggt jede Anfrage strukturiert und verbucht sie in den Metriken.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Default-Header: Antworten enthalten dynamische Daten und sollen nicht
		// gecacht werden (Swiss-Guidelines Quick Win, ADR-019). Handler können
		// dies bei Bedarf überschreiben (z. B. statische Doc-Assets).
		rec.Header().Set("Cache-Control", "no-store")

		next.ServeHTTP(rec, r)

		dur := time.Since(start)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		s.metrics.ObserveRequest(r.Method, route, rec.status, dur)
		s.logger.Info("request",
			"method", r.Method,
			"route", route,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", float64(dur.Microseconds())/1000,
		)
	})
}

// problemFallback ersetzt die Klartext-Antworten des ServeMux durch clios
// einheitliches problem+json (RFC 7807, ADR-019). Ohne sie beantwortet der Mux
// unbekannte Pfade mit „404 page not found" als text/plain — als einzige Route
// der gesamten API bricht das den dokumentierten Fehler-Contract.
//
// Praktische Wirkung über die Konsistenz hinaus: MCP-Clients (Claude Code u. a.)
// tasten beim Verbinden optionale Pfade ab — allen voran die OAuth-Discovery
// unter /.well-known/… . Ein Klartext-404 lässt ältere Clients daran scheitern,
// die Antwort als JSON-Fehlerobjekt zu lesen, statt sauber ohne Auth
// weiterzumachen. Ein wohlgeformtes JSON-404 ist für sie ein klares „gibt es
// hier nicht".
//
// Ersetzt wird ausschließlich der Body von 404/405-Antworten, die als
// text/plain ausgeliefert werden — also genau die Antworten aus http.Error /
// http.NotFound. Status, Allow-Header und alle Handler-eigenen Fehler
// (writeError schreibt bereits problem+json) bleiben unverändert.
func problemFallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&problemWriter{ResponseWriter: w}, r)
	})
}

// plainTextContentType ist der Content-Type, den http.Error/http.NotFound setzen.
const plainTextContentType = "text/plain; charset=utf-8"

// problemWriter schreibt den Body von Klartext-404/405 als problem+json neu.
type problemWriter struct {
	http.ResponseWriter
	replaced bool
}

func (p *problemWriter) WriteHeader(code int) {
	if (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		p.Header().Get("Content-Type") == plainTextContentType {
		p.replaced = true
		p.Header().Set("Content-Type", problemContentType)
		p.Header().Del("Content-Length")
		p.ResponseWriter.WriteHeader(code)
		_ = json.NewEncoder(p.ResponseWriter).Encode(problemDetails{
			Type:   "about:blank",
			Title:  http.StatusText(code),
			Status: code,
			Detail: problemDetail(code),
		})
		return
	}
	p.ResponseWriter.WriteHeader(code)
}

// Write verwirft den ursprünglichen Klartext-Body, wenn er bereits durch
// problem+json ersetzt wurde; sonst reicht er durch.
func (p *problemWriter) Write(b []byte) (int, error) {
	if p.replaced {
		return len(b), nil
	}
	return p.ResponseWriter.Write(b)
}

func (p *problemWriter) Flush() {
	if f, ok := p.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gibt den umschlossenen ResponseWriter frei — wie beim statusRecorder
// nötig, damit http.ResponseController die Schreib-Deadlines der Stream-Handler
// erreicht.
func (p *problemWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// problemDetail formuliert die Meldung zum Fallback-Status. Bewusst ohne
// Rückgabe des angefragten Pfads oder der erlaubten Methoden im Body — der
// Allow-Header trägt letzteres bereits, und der Body bleibt frei von
// gespiegelter Eingabe.
func problemDetail(code int) string {
	if code == http.StatusMethodNotAllowed {
		return "methode für diesen endpunkt nicht erlaubt"
	}
	return "kein endpunkt unter diesem pfad"
}
