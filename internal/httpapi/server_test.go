package httpapi

import (
	"bufio"
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
