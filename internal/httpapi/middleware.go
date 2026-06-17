package httpapi

import (
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
