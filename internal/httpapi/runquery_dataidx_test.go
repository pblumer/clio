package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

// newDataIdxServer baut einen Server, dessen Store den deklarierten data-Index
// (ADR-029) führt.
func newDataIdxServer(t *testing.T, fields map[string][]string) *Server {
	t.Helper()
	st, err := store.OpenWithOptions(filepath.Join(t.TempDir(), "test.db"),
		store.Options{SyncMode: store.SyncGroup, DataIndexFields: fields})
	if err != nil {
		t.Fatalf("store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedKey(t, st, testAdminKID, testAdminSecret, auth.StatusActive, auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin)
	return New(config.Config{Addr: ":0"}, st, nil)
}

// TestRunQueryUsesDataIndex prüft, dass eine `event.type == X &&
// event.data.<feld> == '<wert>'`-Query über den Daten-Index korrekt beantwortet
// wird: nur passende Events des Typs, Subject-Scope und Restprädikat angewandt.
func TestRunQueryUsesDataIndex(t *testing.T) {
	srv := newDataIdxServer(t, map[string][]string{"emp.v2": {"department"}})

	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, `{"events":[
		{"source":"s","subject":"/e/1","type":"emp.v2","data":{"department":"support","name":"a"}},
		{"source":"s","subject":"/e/2","type":"emp.v2","data":{"department":"sales","name":"b"}},
		{"source":"s","subject":"/e/3","type":"emp.v2","data":{"department":"support","name":"c"}},
		{"source":"s","subject":"/other/1","type":"other","data":{"department":"support"}}
	]}`)

	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/e/","recursive":true,"where":"event.type == 'emp.v2' && event.data.department == 'support'"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; %s", rec.Code, rec.Body.String())
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 2 {
		t.Fatalf("treffer = %d, want 2 (%s)", len(got), rec.Body.String())
	}
	for _, ev := range got {
		if ev.Type != "emp.v2" {
			t.Fatalf("unerwarteter typ %q", ev.Type)
		}
	}
	// Der Index darf keine Warnung über fehlenden Index auslösen.
	if w := rec.Header().Get("X-Clio-Query-Warning"); w != "" {
		t.Fatalf("unerwartete index-warnung: %q", w)
	}
}

// TestRunQueryDataIndexSubjectScope stellt sicher, dass der Index-Pfad den
// Subject-Scope nachfiltert (der Index ist nur nach Typ+Feld+Wert geordnet).
func TestRunQueryDataIndexSubjectScope(t *testing.T) {
	srv := newDataIdxServer(t, map[string][]string{"emp.v2": {"department"}})

	do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, `{"events":[
		{"source":"s","subject":"/team/a/1","type":"emp.v2","data":{"department":"support"}},
		{"source":"s","subject":"/team/b/1","type":"emp.v2","data":{"department":"support"}}
	]}`)

	rec := do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken,
		`{"subject":"/team/a","recursive":true,"where":"event.type == 'emp.v2' && event.data.department == 'support'"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeNDJSON(t, rec.Body.String())
	if len(got) != 1 || got[0].Subject != "/team/a/1" {
		t.Fatalf("treffer = %+v, want genau /team/a/1", got)
	}
}

// TestRunQueryDataIndexResultMatchesScan vergleicht das Indexergebnis mit dem
// vollständigen Scan-Pfad (nicht deklariertes Feld) — beide müssen identisch sein.
func TestRunQueryDataIndexResultMatchesScan(t *testing.T) {
	body := `{"events":[
		{"source":"s","subject":"/e/1","type":"emp.v2","data":{"department":"support"}},
		{"source":"s","subject":"/e/2","type":"emp.v2","data":{"department":"sales"}},
		{"source":"s","subject":"/e/3","type":"emp.v2","data":{"department":"support"}}
	]}`
	const where = `{"subject":"/e/","recursive":true,"where":"event.type == 'emp.v2' && event.data.department == 'support'"}`

	indexed := newDataIdxServer(t, map[string][]string{"emp.v2": {"department"}})
	scan := newDataIdxServer(t, nil) // gleiche Daten, aber ohne Index → Scan-Pfad
	for _, srv := range []*Server{indexed, scan} {
		do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body)
	}

	idxRes := decodeNDJSON(t, do(t, indexed, http.MethodPost, "/api/v1/run-query", adminToken, where).Body.String())
	scanRes := decodeNDJSON(t, do(t, scan, http.MethodPost, "/api/v1/run-query", adminToken, where).Body.String())
	if len(idxRes) != len(scanRes) || len(idxRes) != 2 {
		t.Fatalf("index=%d scan=%d, want beide 2", len(idxRes), len(scanRes))
	}
	for i := range idxRes {
		if idxRes[i].ID != scanRes[i].ID {
			t.Fatalf("ergebnis[%d]: index=%s scan=%s", i, idxRes[i].ID, scanRes[i].ID)
		}
	}
}
