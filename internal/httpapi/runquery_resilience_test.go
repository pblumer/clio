package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
)

// countBlankLines zählt die reinen Heartbeat-Leerzeilen (nach Trim leer) im
// NDJSON-Body. Die führende sofort-geflushte Leerzeile zählt mit.
func countBlankLines(body string) int {
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == "" {
			n++
		}
	}
	return n
}

// TestRunQueryHeartbeatBeforeFirstHit deckt die gemeldete 502-Ursache ab: bei
// einem selektiven Prädikat über einen breiten Scope floss bis zum Scan-Ende
// kein Byte (Time-to-first-byte = Scan-Dauer). Jetzt sendet der Scan periodisch
// eine Leerzeile, sodass Header und Lebenszeichen sofort/laufend fließen.
func TestRunQueryHeartbeatBeforeFirstHit(t *testing.T) {
	old := queryHeartbeat
	queryHeartbeat = time.Nanosecond // feuert bei jedem gescannten Event
	defer func() { queryHeartbeat = old }()

	srv := newTestServer(t)
	// Mehrere Events, von denen KEINES das Prädikat erfüllt → voller Scan ohne
	// Treffer. Ohne Heartbeat bliebe der Body leer, bis der Scan endet.
	var b strings.Builder
	b.WriteString(`{"events":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"source":"s","subject":"/employees/x","type":"hired","data":{"lastName":"Other"}}`)
	}
	b.WriteString(`]}`)
	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, b.String())

	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/employees/","recursive":true,"where":"event.data.lastName == 'User25199'"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; %s", rec.Code, rec.Body.String())
	}
	if got := len(decodeNDJSON(t, rec.Body.String())); got != 0 {
		t.Fatalf("treffer = %d, want 0", got)
	}
	// Heartbeat-Bytes flossen während des Scans (führende Leerzeile + Scan-Beats).
	if bl := countBlankLines(rec.Body.String()); bl < 2 {
		t.Fatalf("heartbeat-leerzeilen = %d, want >= 2 (kein lebenszeichen während des scans)", bl)
	}
}

// TestRunQueryImmediateFlush prüft, dass auch eine sofort treffende Query mit
// langem (Default) Heartbeat-Intervall ein sofortiges Lebenszeichen sendet — die
// führende Leerzeile, die die Header umgehend an den Proxy gibt.
func TestRunQueryImmediateFlush(t *testing.T) {
	old := queryHeartbeat
	queryHeartbeat = time.Hour // soll im Test NICHT zwischendurch feuern
	defer func() { queryHeartbeat = old }()

	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"s","subject":"/a/1","type":"t","data":{"k":1}}]}`)

	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/a","recursive":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := len(decodeNDJSON(t, rec.Body.String())); got != 1 {
		t.Fatalf("treffer = %d, want 1", got)
	}
	if !strings.HasPrefix(rec.Body.String(), "\n") {
		t.Fatalf("body beginnt nicht mit sofort-geflushter leerzeile: %q", rec.Body.String()[:min(10, len(rec.Body.String()))])
	}
}

// TestRunQueryDeadlineAborts prüft, dass eine konfigurierte Query-Deadline einen
// laufenden Scan sauber abbricht (definiertes, ggf. unvollständiges Ergebnis mit
// Status 200) statt die Verbindung hängen zu lassen.
func TestRunQueryDeadlineAborts(t *testing.T) {
	srv := newTestServerCfg(t, config.Config{Addr: ":0", QueryTimeout: time.Nanosecond})

	var b strings.Builder
	b.WriteString(`{"events":[`)
	for i := 0; i < 100; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"source":"s","subject":"/a/1","type":"t"}`)
	}
	b.WriteString(`]}`)
	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, b.String())

	// Leeres Prädikat = alle im Scope; die 1ns-Deadline ist beim ersten guard
	// längst überschritten → der Scan bricht ab, bevor alle Events fließen.
	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/a","recursive":true,"limit":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stream bereits begonnen)", rec.Code)
	}
	if got := len(decodeNDJSON(t, rec.Body.String())); got >= 100 {
		t.Fatalf("treffer = %d, want < 100 (deadline hätte den scan kürzen müssen)", got)
	}
}

// TestRunQueryDeadlineDisabledByDefault stellt sicher, dass ohne konfigurierte
// Deadline (Default 0) weiterhin der gesamte Scope geliefert wird.
func TestRunQueryDeadlineDisabledByDefault(t *testing.T) {
	srv := newTestServer(t) // QueryTimeout 0

	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[
			{"source":"s","subject":"/a/1","type":"t"},
			{"source":"s","subject":"/a/2","type":"t"},
			{"source":"s","subject":"/a/3","type":"t"}
		]}`)

	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/a","recursive":true}`)
	if got := len(decodeNDJSON(t, rec.Body.String())); got != 3 {
		t.Fatalf("treffer = %d, want 3 (ohne deadline kein abbruch)", got)
	}
}

// TestRunQueryIndexWarningHeader prüft den Warn-Header: er erscheint genau dann,
// wenn das Prädikat keinen Typ-Index nutzen kann (kein event.type-Constraint),
// und nennt den data-Scan, wenn das Prädikat event.data anfasst.
func TestRunQueryIndexWarningHeader(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"s","subject":"/employees/1","type":"hired","data":{"lastName":"Ada"}}]}`)

	const hdr = "X-Clio-Query-Warning"
	cases := []struct {
		name      string
		where     string
		wantWarn  bool
		wantsData bool
	}{
		{"data ohne typ-constraint", "event.data.lastName == 'User25199'", true, true},
		{"nicht-data ohne typ-constraint", "event.subject == '/x'", true, false},
		{"mit typ-constraint", "event.type == 'hired' && event.data.lastName == 'Ada'", false, false},
		{"leeres prädikat", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"subject":"/","recursive":true`
			if tc.where != "" {
				body += `,"where":` + jsonString(tc.where)
			}
			body += `}`
			rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; %s", rec.Code, rec.Body.String())
			}
			warn := rec.Header().Get(hdr)
			if tc.wantWarn && warn == "" {
				t.Fatalf("%s fehlt, erwartet bei nicht-indizierbarer query", hdr)
			}
			if !tc.wantWarn && warn != "" {
				t.Fatalf("%s = %q, erwartet leer (index nutzbar)", hdr, warn)
			}
			if tc.wantWarn {
				if tc.wantsData && !strings.Contains(warn, "data") {
					t.Fatalf("%s = %q, erwartet data-hinweis", hdr, warn)
				}
				if !tc.wantsData && strings.Contains(warn, "data") {
					t.Fatalf("%s = %q, kein data-hinweis erwartet", hdr, warn)
				}
			}
		})
	}
}

// jsonString liefert ein minimal escaptes JSON-String-Literal für die Test-Bodies.
func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// TestRunQueryUnderConcurrentWriteLoad reproduziert den gemeldeten Fall über einen
// echten HTTP-Server: ein voller Daten-Prädikat-Scan über /employees/ läuft,
// während parallel weiter Events geschrieben werden. Erwartet wird ein definiertes
// Ergebnis (Status 200, der eine Treffer, laufende Heartbeat-Bytes) statt eines
// hängenden Streams/502.
func TestRunQueryUnderConcurrentWriteLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("lasttest in -short übersprungen")
	}
	old := queryHeartbeat
	queryHeartbeat = 5 * time.Millisecond // im Test sichtbar machen
	defer func() { queryHeartbeat = old }()

	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Basisbestand direkt über den Store seeden (schnell). Der gesuchte Treffer
	// liegt am Ende → praktisch der ganze Scope wird gescannt.
	const seed = 40000
	const block = 2000
	data, _ := json.Marshal(map[string]any{"lastName": "Other", "dept": "ops"})
	matchData, _ := json.Marshal(map[string]any{"lastName": "User25199", "dept": "ops"})
	for start := 0; start < seed; start += block {
		cands := make([]event.Candidate, 0, block)
		for i := start; i < start+block && i < seed; i++ {
			d := data
			if i == seed-1 {
				d = matchData
			}
			cands = append(cands, event.Candidate{
				Source: "load", Subject: fmt.Sprintf("/employees/e-%06d", i),
				Type: "hired", Data: d,
			})
		}
		if _, err := srv.store.Append(cands, nil); err != nil {
			t.Fatalf("seed append: %v", err)
		}
	}

	// Hintergrund-Schreiblast: schreibt weiter, bis der Test stoppt.
	stop := make(chan struct{})
	var writes int64
	go func() {
		seq := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			cands := make([]event.Candidate, 0, 200)
			for i := 0; i < 200; i++ {
				cands = append(cands, event.Candidate{
					Source: "live", Subject: fmt.Sprintf("/employees/live-%06d", seq),
					Type: "hired", Data: data,
				})
				seq++
			}
			if _, err := srv.store.Append(cands, nil); err != nil {
				return
			}
			atomic.AddInt64(&writes, int64(len(cands)))
		}
	}()
	defer close(stop)

	// Die gemeldete Query über einen echten Client mit großzügigem Timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := `{"subject":"/employees/","recursive":true,"limit":100000,"where":"event.data.lastName == 'User25199'"}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/run-query", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("run-query unter last: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if w := resp.Header.Get("X-Clio-Query-Warning"); w == "" {
		t.Fatalf("erwarteter index-warn-header fehlt (data-prädikat ohne typ-constraint)")
	}

	// Den gesamten Stream lesen — er muss innerhalb des Timeouts enden (kein Hänger).
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("stream lesen: %v", err)
	}
	// Genau ein Treffer (User25199); Blankzeilen sind Heartbeats und werden ignoriert.
	hits := 0
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev event.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("ndjson-zeile: %v (%q)", err, line)
		}
		hits++
	}
	if hits != 1 {
		t.Fatalf("treffer = %d, want 1", hits)
	}
	t.Logf("parallele writes während der query: %d", atomic.LoadInt64(&writes))
}
