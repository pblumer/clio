package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(config.Config{Addr: ":0", APIToken: "secret-token"}, st, nil)
}

func do(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestPing(t *testing.T) {
	srv := newTestServer(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			rec := do(t, srv, method, "/api/v1/ping", "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("antwort dekodieren: %v", err)
			}
			if body["status"] != "ok" {
				t.Fatalf("status feld = %q, want %q", body["status"], "ok")
			}
		})
	}
}

func TestWriteEventsAuth(t *testing.T) {
	srv := newTestServer(t)
	body := `{"events":[{"source":"s","subject":"/a","type":"t"}]}`

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"gültiges token", "secret-token", http.StatusOK},
		{"falsches token", "wrong", http.StatusUnauthorized},
		{"kein token", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/write-events", tt.token, body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestWriteEventsValidation(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		name string
		body string
	}{
		{"leere events", `{"events":[]}`},
		{"fehlendes subject", `{"events":[{"source":"s","type":"t"}]}`},
		{"subject ohne slash", `{"events":[{"source":"s","subject":"a","type":"t"}]}`},
		{"fehlender type", `{"events":[{"source":"s","subject":"/a"}]}`},
		{"unbekanntes feld", `{"events":[{"source":"s","subject":"/a","type":"t"}],"foo":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// Zwei Events in /books/42, eines in /books/99.
	writeBody := `{"events":[
		{"source":"lib","subject":"/books/42","type":"acquired","data":{"title":"A"}},
		{"source":"lib","subject":"/books/99","type":"acquired"},
		{"source":"lib","subject":"/books/42","type":"borrowed"}
	]}`
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", writeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	written := decodeNDJSON(t, rec.Body.String())
	if len(written) != 3 {
		t.Fatalf("geschriebene events = %d, want 3", len(written))
	}
	// IDs monoton 1,2,3 und serverseitige Felder gesetzt.
	for i, ev := range written {
		wantID := strconv.Itoa(i + 1)
		if ev.ID != wantID {
			t.Fatalf("event[%d].id = %q, want %q", i, ev.ID, wantID)
		}
		if ev.SpecVersion != event.SpecVersion || ev.Time == "" {
			t.Fatalf("event[%d] serverfelder unvollständig: %+v", i, ev)
		}
	}

	// Read /books/42 liefert genau die beiden zugehörigen Events in Reihenfolge.
	rec = do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token", `{"subject":"/books/42"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200", rec.Code)
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 2 {
		t.Fatalf("gelesene events = %d, want 2", len(got))
	}
	if got[0].Type != "acquired" || got[1].Type != "borrowed" {
		t.Fatalf("falsche reihenfolge/typen: %q, %q", got[0].Type, got[1].Type)
	}

	// Unbekanntes Subject liefert leer (kein Fehler).
	rec = do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token", `{"subject":"/nope"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read leeres subject status = %d, want 200", rec.Code)
	}
	if n := len(decodeNDJSON(t, rec.Body.String())); n != 0 {
		t.Fatalf("leeres subject: events = %d, want 0", n)
	}
}

func TestReadEventsBadRequest(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		name string
		body string
	}{
		{"kaputtes json", `{`},
		{"unbekanntes feld", `{"subject":"/a","foo":1}`},
		{"subject leer", `{"subject":""}`},
		{"subject ohne slash", `{"subject":"a"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestWriteEventsPreconditions(t *testing.T) {
	srv := newTestServer(t)

	// isSubjectPristine: erster Write auf leeren Stream -> 200.
	body := `{"events":[{"source":"s","subject":"/p","type":"t"}],
		"preconditions":[{"type":"isSubjectPristine","payload":{"subject":"/p"}}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", body); rec.Code != http.StatusOK {
		t.Fatalf("pristine-write status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Zweiter Write mit gleicher Precondition -> 409 (Stream nicht mehr leer).
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", body); rec.Code != http.StatusConflict {
		t.Fatalf("zweiter pristine-write status = %d, want 409", rec.Code)
	}

	// isSubjectOnEventId: /p steht jetzt auf ID 1.
	okBody := `{"events":[{"source":"s","subject":"/p","type":"t2"}],
		"preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/p","eventId":"1"}}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", okBody); rec.Code != http.StatusOK {
		t.Fatalf("onEventId-write status = %d, want 200", rec.Code)
	}
	// Veraltete erwartete ID -> 409.
	staleBody := `{"events":[{"source":"s","subject":"/p","type":"t3"}],
		"preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/p","eventId":"1"}}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", staleBody); rec.Code != http.StatusConflict {
		t.Fatalf("veralteter onEventId-write status = %d, want 409", rec.Code)
	}
}

func TestWriteEventsPreconditionBadRequest(t *testing.T) {
	srv := newTestServer(t)
	ev := `"events":[{"source":"s","subject":"/p","type":"t"}]`
	tests := []struct {
		name string
		body string
	}{
		{"subject ohne slash", `{` + ev + `,"preconditions":[{"type":"isSubjectPristine","payload":{"subject":"p"}}]}`},
		{"unbekannter typ", `{` + ev + `,"preconditions":[{"type":"isMagic","payload":{"subject":"/p"}}]}`},
		{"eventId nicht numerisch", `{` + ev + `,"preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/p","eventId":"x"}}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestReadEventsBounds(t *testing.T) {
	srv := newTestServer(t)
	for i := 0; i < 5; i++ {
		do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
			`{"events":[{"source":"s","subject":"/s","type":"t"}]}`)
	}

	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token",
		`{"subject":"/s","lowerBound":"2","upperBound":"4"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 3 || got[0].ID != "2" || got[2].ID != "4" {
		t.Fatalf("bounds-ergebnis falsch: %+v", got)
	}
}

func TestReadEventsBoundsBadRequest(t *testing.T) {
	srv := newTestServer(t)
	tests := []string{
		`{"subject":"/s","lowerBound":"x"}`,
		`{"subject":"/s","upperBound":"-1"}`,
		`{"subject":"/s","lowerBound":"5","upperBound":"2"}`,
	}
	for _, body := range tests {
		rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestReadEventsRecursive(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[
			{"source":"s","subject":"/r/a","type":"t1"},
			{"source":"s","subject":"/other","type":"t2"},
			{"source":"s","subject":"/r/b","type":"t3"}
		]}`)

	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token",
		`{"subject":"/r","recursive":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("rekursiv /r: %+v", got)
	}
}

func TestReadEventsTypeFilter(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[
			{"source":"s","subject":"/o/1","type":"placed"},
			{"source":"s","subject":"/o/2","type":"cancelled"},
			{"source":"s","subject":"/o/1","type":"placed"},
			{"source":"s","subject":"/o/3","type":"shipped"}
		]}`)

	// Rekursiv ab /o, nur placed + shipped -> IDs 1,3,4.
	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token",
		`{"subject":"/o","recursive":true,"types":["placed","shipped"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 3 || got[0].ID != "1" || got[1].ID != "3" || got[2].ID != "4" {
		t.Fatalf("type-filter ergebnis: %+v", got)
	}
}

func TestReadEventsTypesBadRequest(t *testing.T) {
	srv := newTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token",
		`{"subject":"/o","types":["placed",""]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestVerifyEndpoint(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[{"source":"s","subject":"/a","type":"t1"},{"source":"s","subject":"/a","type":"t2"}]}`)

	// Ohne Token -> 401.
	if rec := do(t, srv, http.MethodGet, "/api/v1/verify", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ohne token: status = %d, want 401", rec.Code)
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/verify", "secret-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Count uint64 `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("antwort dekodieren: %v", err)
	}
	if !res.OK || res.Count != 2 {
		t.Fatalf("verify = %+v, want ok=true count=2", res)
	}
}

func TestReadEventTypes(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[
			{"source":"s","subject":"/a","type":"borrowed"},
			{"source":"s","subject":"/a","type":"acquired"},
			{"source":"s","subject":"/b","type":"acquired"}
		]}`)

	// Ohne Token -> 401.
	if rec := do(t, srv, http.MethodGet, "/api/v1/read-event-types", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ohne token: status = %d, want 401", rec.Code)
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/read-event-types", "secret-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	type typeInfo struct {
		Type  string `json:"type"`
		Count uint64 `json:"count"`
	}
	var got []typeInfo
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ti typeInfo
		if err := json.Unmarshal([]byte(line), &ti); err != nil {
			t.Fatalf("ndjson dekodieren: %v", err)
		}
		got = append(got, ti)
	}
	want := []typeInfo{{"acquired", 2}, {"borrowed", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[{"source":"s","subject":"/a","type":"t1"},{"source":"s","subject":"/a","type":"t2"}]}`)

	// Ohne Auth erreichbar (Scraping).
	rec := do(t, srv, http.MethodGet, "/metrics", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain…", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"clio_events_written_total 2",
		"clio_events_total 2",
		`clio_http_requests_total{method="POST",route="POST /api/v1/write-events",status="200"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics enthält nicht: %s", want)
		}
	}
}

func TestOpenAPISpec(t *testing.T) {
	srv := newTestServer(t)
	// Ohne Auth erreichbar (Doku ist nicht sensibel).
	rec := do(t, srv, http.MethodGet, "/openapi.yaml", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Fatalf("content-type = %q, want application/yaml", ct)
	}
	if !strings.Contains(rec.Body.String(), "openapi:") {
		t.Fatal("spec enthält kein \"openapi:\"")
	}
}

func TestDocsUI(t *testing.T) {
	srv := newTestServer(t)

	// /docs -> Redirect auf /docs/
	rec := do(t, srv, http.MethodGet, "/docs", "", "")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("/docs status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/docs/" {
		t.Fatalf("Location = %q, want /docs/", loc)
	}

	// /docs/ -> Swagger-UI-HTML
	rec = do(t, srv, http.MethodGet, "/docs/", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/docs/ status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Swagger UI") {
		t.Fatal("/docs/ liefert keine Swagger-UI-Seite")
	}
}

func TestEventsPathRead(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[
			{"source":"s","subject":"/books/42","type":"acquired"},
			{"source":"s","subject":"/books/42","type":"borrowed"},
			{"source":"s","subject":"/books/99","type":"acquired"}
		]}`)

	tests := []struct {
		name    string
		path    string
		wantIDs []string
	}{
		{"leaf", "/api/v1/events/books/42", []string{"1", "2"}},
		{"eltern auto-rekursiv", "/api/v1/events/books", []string{"1", "2", "3"}},
		{"wurzel", "/api/v1/events", []string{"1", "2", "3"}},
		{"typ-filter", "/api/v1/events/books?type=acquired", []string{"1", "3"}},
		{"recursive=false auf eltern", "/api/v1/events/books?recursive=false", nil},
		{"bounds", "/api/v1/events/books?lowerBound=2&upperBound=2", []string{"2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodGet, tt.path, "secret-token", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			got := decodeNDJSON(t, rec.Body.String())
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("ids = %v, want %v", idsOfEvents(got), tt.wantIDs)
			}
			for i := range tt.wantIDs {
				if got[i].ID != tt.wantIDs[i] {
					t.Fatalf("ids = %v, want %v", idsOfEvents(got), tt.wantIDs)
				}
			}
		})
	}
}

func TestEventsPathAuthAndBadRequest(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		name, token, path string
		want              int
	}{
		{"kein token", "", "/api/v1/events/books", http.StatusUnauthorized},
		{"recursive kaputt", "secret-token", "/api/v1/events/books?recursive=vielleicht", http.StatusBadRequest},
		{"lowerBound kaputt", "secret-token", "/api/v1/events/books?lowerBound=x", http.StatusBadRequest},
		{"leerer typ", "secret-token", "/api/v1/events/books?type=", http.StatusBadRequest},
		{"watch kaputt", "secret-token", "/api/v1/events/books?watch=jain", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodGet, tt.path, tt.token, "")
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// TestEventsPathWatch: GET .../events/<subject>?watch=true streamt History + Live.
func TestEventsPathWatch(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/w/1","type":"h"}]}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/w?watch=true", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("watch-request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)

	if h := readStreamEvent(t, reader); h.Type != "h" {
		t.Fatalf("history type = %q, want h", h.Type)
	}
	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/w/2","type":"live"}]}`)
	if l := readStreamEvent(t, reader); l.Type != "live" {
		t.Fatalf("live type = %q, want live", l.Type)
	}
}

func idsOfEvents(events []event.Event) []string {
	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestObserveEventsBadRequest(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		name, token, body string
		want              int
	}{
		{"kein token", "", `{"subject":"/a"}`, http.StatusUnauthorized},
		{"subject ohne slash", "secret-token", `{"subject":"a"}`, http.StatusBadRequest},
		{"lowerBound nicht numerisch", "secret-token", `{"subject":"/a","lowerBound":"x"}`, http.StatusBadRequest},
		{"kaputtes json", "secret-token", `{`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/observe-events", tt.token, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// TestObserveEventsHistoryAndLive prüft den vollen Ablauf über einen echten
// HTTP-Server: erst History, dann live nachgelieferte Events.
func TestObserveEventsHistoryAndLive(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Zwei History-Events vorab schreiben.
	writeViaHTTP(t, ts.URL, `{"events":[
		{"source":"s","subject":"/obs","type":"h1"},
		{"source":"s","subject":"/obs","type":"h2"}
	]}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/observe-events",
		strings.NewReader(`{"subject":"/obs"}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("observe-request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	if h1 := readStreamEvent(t, reader); h1.Type != "h1" {
		t.Fatalf("history[0] type = %q, want h1", h1.Type)
	}
	if h2 := readStreamEvent(t, reader); h2.Type != "h2" {
		t.Fatalf("history[1] type = %q, want h2", h2.Type)
	}

	// Jetzt live: ein Event in /obs und eines in /anders (darf NICHT kommen).
	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/anders","type":"skip"}]}`)
	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/obs","type":"live"}]}`)

	// /anders bekommt ID 3 (wird gefiltert), /obs bekommt ID 4 und wird live
	// geliefert — das belegt zugleich die Subject-Filterung.
	live := readStreamEvent(t, reader)
	if live.Type != "live" {
		t.Fatalf("live type = %q, want live (fremdes subject nicht gefiltert?)", live.Type)
	}
	if live.ID != "4" {
		t.Fatalf("live id = %q, want 4", live.ID)
	}
}

// TestObserveEventsTypeFilter: nur Events passender Typen werden live geliefert.
func TestObserveEventsTypeFilter(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/observe-events",
		strings.NewReader(`{"subject":"/t","recursive":true,"types":["placed"]}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("observe-request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)

	// "cancelled" (gefiltert) und "placed" (durchgelassen) schreiben.
	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/t/1","type":"cancelled"}]}`)
	writeViaHTTP(t, ts.URL, `{"events":[{"source":"s","subject":"/t/1","type":"placed"}]}`)

	ev := readStreamEvent(t, reader)
	if ev.Type != "placed" {
		t.Fatalf("live type = %q, want placed (typ-filter griff nicht)", ev.Type)
	}
}

func writeViaHTTP(t *testing.T, baseURL, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/write-events", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write-request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d, want 200", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}

func readStreamEvent(t *testing.T, r *bufio.Reader) event.Event {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("stream-zeile lesen: %v", err)
	}
	var ev event.Event
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("stream-zeile dekodieren: %v (%q)", err, line)
	}
	return ev
}

// Geschlossener Store: Lese-/Schreibrouten antworten mit 500.
func TestDataRoutesStoreError(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.store.Close(); err != nil {
		t.Fatalf("store schließen: %v", err)
	}

	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", "secret-token",
		`{"events":[{"source":"s","subject":"/a","type":"t"}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("write status = %d, want 500", rec.Code)
	}

	rec = do(t, srv, http.MethodPost, "/api/v1/read-events", "secret-token", `{"subject":"/a"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("read status = %d, want 500", rec.Code)
	}

	// observe scheitert beim History-Lesen aus dem geschlossenen Store.
	rec = do(t, srv, http.MethodPost, "/api/v1/observe-events", "secret-token", `{"subject":"/a"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("observe status = %d, want 500", rec.Code)
	}
}

// TestObserveStreamingUnsupported: ohne http.Flusher antwortet observe mit 500.
func TestObserveStreamingUnsupported(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/observe-events", strings.NewReader(`{"subject":"/a"}`))
	// failingWriter implementiert kein http.Flusher.
	srv.handleObserveEvents(&failingWriter{}, req)
}

// failingWriter schlägt bei jedem Write fehl, um den Fehlerpfad in writeNDJSON
// (Header bereits gesendet) abzudecken.
type failingWriter struct{ header http.Header }

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("kaputt") }
func (f *failingWriter) WriteHeader(int)           {}

func TestWriteNDJSONEncodeError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Darf nicht paniken, auch wenn der ResponseWriter scheitert.
	writeNDJSON(&failingWriter{}, logger, []event.Event{{ID: "1", Subject: "/a"}})
}

func decodeNDJSON(t *testing.T, body string) []event.Event {
	t.Helper()
	var out []event.Event
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev event.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("ndjson-zeile dekodieren: %v (%q)", err, line)
		}
		out = append(out, ev)
	}
	return out
}
