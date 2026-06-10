package metrics

import (
	"strings"
	"testing"
	"time"
)

func render(m *Metrics, g Gauges) string {
	var sb strings.Builder
	m.Write(&sb, g)
	return sb.String()
}

func TestRenderContainsAllSeries(t *testing.T) {
	m := New()
	m.ObserveRequest("POST", "POST /api/v1/write-events", 200, 5*time.Millisecond)
	m.ObserveRequest("POST", "POST /api/v1/write-events", 200, 20*time.Millisecond)
	m.ObserveRequest("GET", "GET /api/v1/verify", 200, time.Millisecond)
	m.AddEventsWritten(3)
	m.IncPreconditionFailure()

	out := render(m, Gauges{ActiveObservers: 2, EventsTotal: 42})

	wants := []string{
		`clio_http_requests_total{method="POST",route="POST /api/v1/write-events",status="200"} 2`,
		`clio_http_requests_total{method="GET",route="GET /api/v1/verify",status="200"} 1`,
		"clio_http_request_duration_seconds_count 3",
		`clio_http_request_duration_seconds_bucket{le="+Inf"} 3`,
		"clio_events_written_total 3",
		"clio_precondition_failures_total 1",
		"clio_active_observers 2",
		"clio_events_total 42",
		"# TYPE clio_http_requests_total counter",
		"# TYPE clio_active_observers gauge",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("metrics-ausgabe enthält nicht:\n  %s\n--- ausgabe ---\n%s", w, out)
		}
	}
}

func TestHistogramBucketsCumulative(t *testing.T) {
	m := New()
	// 1ms fällt in alle Buckets ab le=0.001; 2s nur ab le=2.5.
	m.ObserveRequest("GET", "/x", 200, time.Millisecond)
	m.ObserveRequest("GET", "/x", 200, 2*time.Second)

	out := render(m, Gauges{})
	// le=0.005 muss genau 1 enthalten (nur die 1ms-Anfrage).
	if !strings.Contains(out, `clio_http_request_duration_seconds_bucket{le="0.005"} 1`) {
		t.Errorf("le=0.005 falsch:\n%s", out)
	}
	// le=+Inf muss beide enthalten.
	if !strings.Contains(out, `clio_http_request_duration_seconds_bucket{le="+Inf"} 2`) {
		t.Errorf("le=+Inf falsch:\n%s", out)
	}
}
