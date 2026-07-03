package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestObserveSurvivesWriteTimeout ist der Regressionstest zu K1: der
// statusRecorder der instrument()-Middleware muss Unwrap() anbieten, sonst kann
// http.ResponseController die WriteTimeout des Servers nicht je Verbindung
// aufheben und der bewusst langlaufende observe-Stream würde nach WriteTimeout
// hart getrennt.
//
// Der Test fährt einen echten http.Server mit einer sehr kurzen WriteTimeout
// hoch, öffnet einen observe-Stream, wartet deutlich über die WriteTimeout
// hinaus und schreibt danach ein Event. Der Stream muss dieses Event noch
// ausliefern — ohne den Unwrap-Fix hätte der Server die Verbindung längst
// gekappt.
func TestObserveSurvivesWriteTimeout(t *testing.T) {
	srv := newTestServer(t)

	const writeTimeout = 300 * time.Millisecond
	httpSrv := &http.Server{
		Handler:      srv.Handler(),
		WriteTimeout: writeTimeout,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})
	base := "http://" + ln.Addr().String()

	// observe-Stream öffnen (ohne History, wartet auf Live-Events).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/observe-events",
		strings.NewReader(`{"subject":"/k1"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("observe öffnen: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("observe status = %d, want 200", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)

	// Deutlich über die WriteTimeout hinaus warten, DANN erst schreiben. Ohne den
	// Fix ist die Verbindung an dieser Stelle bereits vom Server geschlossen.
	time.Sleep(writeTimeout + 400*time.Millisecond)

	writeBody := `{"events":[{"source":"k1","subject":"/k1","type":"io.k1.ping","data":{"n":1}}]}`
	wrec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, writeBody)
	if wrec.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200", wrec.Code)
	}

	// Das Live-Event muss über den Stream ankommen. Leerzeilen sind Heartbeat/
	// Preamble und werden übersprungen. Der Kontext-Timeout (5 s) bricht ein
	// hängendes Lesen ab; eine vom Server gekappte Verbindung liefert einen
	// Lesefehler — genau die K1-Regression.
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("stream lesen nach WriteTimeout fehlgeschlagen (K1-Regression: Verbindung gekappt?): %v", err)
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue // Heartbeat/Preamble
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			t.Fatalf("event dekodieren: %v (zeile=%q)", err, trimmed)
		}
		if ev.Type == "io.k1.ping" {
			return // Erfolg: Stream lebte über die WriteTimeout hinaus.
		}
	}
}
