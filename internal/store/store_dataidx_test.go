package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// openTempData öffnet einen Store mit deklarierten data-Index-Feldern (ADR-029).
func openTempData(t *testing.T, fields map[string][]string) *Store {
	t.Helper()
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "test.db"),
		Options{SyncMode: SyncGroup, DataIndexFields: fields})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// collectByDataField sammelt die Event-IDs eines Daten-Index-Lookups.
func collectByDataField(t *testing.T, st *Store, typ, field, value string) []string {
	t.Helper()
	var ids []string
	err := st.ReadByDataFieldFunc(typ, field, value, ReadOptions{}, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return true
	})
	if err != nil {
		t.Fatalf("ReadByDataFieldFunc: %v", err)
	}
	return ids
}

func data(t *testing.T, kv map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(kv)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	return b
}

func TestDataIndexLookupOnWrite(t *testing.T) {
	st := openTempData(t, map[string][]string{"emp.v2": {"department"}})

	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/e/1", Type: "emp.v2", Data: data(t, map[string]any{"department": "support", "name": "a"})},
		event.Candidate{Source: "s", Subject: "/e/2", Type: "emp.v2", Data: data(t, map[string]any{"department": "sales", "name": "b"})},
		event.Candidate{Source: "s", Subject: "/e/3", Type: "emp.v2", Data: data(t, map[string]any{"department": "support", "name": "c"})},
		// Anderer Typ mit gleichem Feldwert: darf NICHT im emp.v2-Index landen.
		event.Candidate{Source: "s", Subject: "/x/1", Type: "other", Data: data(t, map[string]any{"department": "support"})},
	)

	got := collectByDataField(t, st, "emp.v2", "department", "support")
	want := []string{"1", "3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("support-Treffer = %v, want %v", got, want)
	}
	// Reihenfolge ist die globale Sequenz.
	if got := collectByDataField(t, st, "emp.v2", "department", "sales"); len(got) != 1 || got[0] != "2" {
		t.Fatalf("sales-Treffer = %v, want [2]", got)
	}
	// Nicht deklariertes Feld ist nicht indiziert.
	if st.DataFieldIndexed("emp.v2", "name") {
		t.Fatal("name sollte nicht indiziert sein")
	}
	if !st.DataFieldIndexed("emp.v2", "department") {
		t.Fatal("department sollte indiziert sein")
	}
}

// Ein Wert darf nicht der Präfix eines anderen sein (Längen-Präfix im Schlüssel).
func TestDataIndexValuePrefixIsolation(t *testing.T) {
	st := openTempData(t, map[string][]string{"t": {"v"}})
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t", Data: data(t, map[string]any{"v": "a"})},
		event.Candidate{Source: "s", Subject: "/b", Type: "t", Data: data(t, map[string]any{"v": "ab"})},
	)
	if got := collectByDataField(t, st, "t", "v", "a"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("Wert \"a\" = %v, want [1] (kein Präfix-Leak auf \"ab\")", got)
	}
	if got := collectByDataField(t, st, "t", "v", "ab"); len(got) != 1 || got[0] != "2" {
		t.Fatalf("Wert \"ab\" = %v, want [2]", got)
	}
}

func TestDataIndexBounds(t *testing.T) {
	st := openTempData(t, map[string][]string{"t": {"v"}})
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t", Data: data(t, map[string]any{"v": "x"})},
		event.Candidate{Source: "s", Subject: "/b", Type: "t", Data: data(t, map[string]any{"v": "x"})},
		event.Candidate{Source: "s", Subject: "/c", Type: "t", Data: data(t, map[string]any{"v": "x"})},
	)
	var ids []string
	err := st.ReadByDataFieldFunc("t", "v", "x", ReadOptions{LowerBound: 2, UpperBound: 2}, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return true
	})
	if err != nil {
		t.Fatalf("ReadByDataFieldFunc: %v", err)
	}
	if len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("bounds-Treffer = %v, want [2]", ids)
	}
}

// Nicht-String-Werte werden in v1 nicht indiziert (kein Lookup-Treffer).
func TestDataIndexSkipsNonString(t *testing.T) {
	st := openTempData(t, map[string][]string{"t": {"n"}})
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t", Data: data(t, map[string]any{"n": 42})},
	)
	if got := collectByDataField(t, st, "t", "n", "42"); len(got) != 0 {
		t.Fatalf("numerischer Wert sollte nicht indiziert sein, got %v", got)
	}
}

// Ein nachträglich deklariertes Feld wird beim erneuten Öffnen über die
// vorhandenen Events nachindiziert (backfillDataIdx, ADR-029).
func TestDataIndexBackfillOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// 1) Ohne Index schreiben.
	st1, err := OpenWithOptions(path, Options{SyncMode: SyncGroup})
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	appendAll(t, st1,
		event.Candidate{Source: "s", Subject: "/a", Type: "t", Data: data(t, map[string]any{"d": "ops"})},
		event.Candidate{Source: "s", Subject: "/b", Type: "t", Data: data(t, map[string]any{"d": "ops"})},
	)
	if err := st1.Close(); err != nil {
		t.Fatalf("close1: %v", err)
	}

	// 2) Mit deklariertem Feld erneut öffnen → Backfill über die Historie.
	st2, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, DataIndexFields: map[string][]string{"t": {"d"}}})
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	got := collectByDataField(t, st2, "t", "d", "ops")
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("Backfill-Treffer = %v, want [1 2]", got)
	}

	// Neue Writes nach Reopen werden weiter gepflegt.
	appendAll(t, st2, event.Candidate{Source: "s", Subject: "/c", Type: "t", Data: data(t, map[string]any{"d": "ops"})})
	if got := collectByDataField(t, st2, "t", "d", "ops"); len(got) != 3 {
		t.Fatalf("nach neuem Write = %v, want 3 Treffer", got)
	}
}

func TestDataFieldIndexedFalseWhenUndeclared(t *testing.T) {
	st := openTemp(t) // keine Felder deklariert
	if st.DataFieldIndexed("t", "f") {
		t.Fatal("ohne Deklaration darf kein Feld indiziert sein")
	}
}
