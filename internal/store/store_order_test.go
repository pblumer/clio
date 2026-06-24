package store

import (
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// collectRead sammelt die Event-IDs eines ReadFunc-Scans in Reihenfolge.
func collectRead(t *testing.T, st *Store, query string, recursive bool, opts ReadOptions) []string {
	t.Helper()
	var ids []string
	if err := st.ReadFunc(query, recursive, opts, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return true
	}); err != nil {
		t.Fatalf("ReadFunc(%q): %v", query, err)
	}
	return ids
}

func TestReadDescendingOrder(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "x"},   // 1
		event.Candidate{Source: "s", Subject: "/a/1", Type: "y"}, // 2
		event.Candidate{Source: "s", Subject: "/a", Type: "x"},   // 3
		event.Candidate{Source: "s", Subject: "/b", Type: "y"},   // 4
		event.Candidate{Source: "s", Subject: "/a/2", Type: "x"}, // 5
	)

	// Wurzel rekursiv: alle Events, neueste zuerst.
	if got := collectRead(t, st, "/", true, ReadOptions{Descending: true}); !equalIDs(got, []string{"5", "4", "3", "2", "1"}) {
		t.Fatalf("wurzel desc = %v, want [5 4 3 2 1]", got)
	}
	// Aufsteigend bleibt der Default.
	if got := collectRead(t, st, "/", true, ReadOptions{}); !equalIDs(got, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("wurzel asc = %v, want [1 2 3 4 5]", got)
	}

	// Nicht-Wurzel rekursiv (/a deckt /a, /a/1, /a/2): neueste zuerst.
	if got := collectRead(t, st, "/a", true, ReadOptions{Descending: true}); !equalIDs(got, []string{"5", "3", "2", "1"}) {
		t.Fatalf("/a desc = %v, want [5 3 2 1]", got)
	}

	// Nicht-rekursiv (Subject-Index): nur /a, neueste zuerst.
	if got := collectRead(t, st, "/a", false, ReadOptions{Descending: true}); !equalIDs(got, []string{"3", "1"}) {
		t.Fatalf("/a non-rec desc = %v, want [3 1]", got)
	}
}

func TestReadDescendingWithBounds(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 1
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 2
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 3
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 4
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 5
	)

	// Wurzel-Scan absteigend mit Bounds [2,4]: startet an upperBound, bricht unter
	// lowerBound ab.
	if got := collectRead(t, st, "/", true, ReadOptions{Descending: true, LowerBound: 2, UpperBound: 4}); !equalIDs(got, []string{"4", "3", "2"}) {
		t.Fatalf("wurzel desc [2,4] = %v, want [4 3 2]", got)
	}
	// upperBound jenseits des letzten Events → ab dem letzten rückwärts.
	if got := collectRead(t, st, "/", true, ReadOptions{Descending: true, UpperBound: 99}); !equalIDs(got, []string{"5", "4", "3", "2", "1"}) {
		t.Fatalf("wurzel desc upper=99 = %v, want [5..1]", got)
	}
	// Frühabbruch (Limit-Semantik) absteigend liefert die jüngsten Treffer.
	var ids []string
	_ = st.ReadFunc("/", true, ReadOptions{Descending: true}, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return len(ids) < 2
	})
	if !equalIDs(ids, []string{"5", "4"}) {
		t.Fatalf("desc frühabbruch = %v, want [5 4]", ids)
	}
}

func TestReadByTypesDescending(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 1
		event.Candidate{Source: "s", Subject: "/b", Type: "y"}, // 2
		event.Candidate{Source: "s", Subject: "/c", Type: "x"}, // 3
		event.Candidate{Source: "s", Subject: "/d", Type: "z"}, // 4
		event.Candidate{Source: "s", Subject: "/e", Type: "y"}, // 5
	)

	// Ein Typ, absteigend.
	if got := collectByTypes(t, st, []string{"x"}, ReadOptions{Descending: true}); !equalIDs(got, []string{"3", "1"}) {
		t.Fatalf("typ x desc = %v, want [3 1]", got)
	}
	// Mehrere Typen, absteigend (global sortiert).
	if got := collectByTypes(t, st, []string{"x", "y"}, ReadOptions{Descending: true}); !equalIDs(got, []string{"5", "3", "2", "1"}) {
		t.Fatalf("typ x,y desc = %v, want [5 3 2 1]", got)
	}
	// Frühabbruch absteigend: jüngster Treffer zuerst.
	var seen []string
	_ = st.ReadByTypesFunc([]string{"x", "y"}, ReadOptions{Descending: true}, func(ev event.Event) bool {
		seen = append(seen, ev.ID)
		return false
	})
	if !equalIDs(seen, []string{"5"}) {
		t.Fatalf("typ desc frühabbruch = %v, want [5]", seen)
	}
}

func TestReadByDataFieldDescending(t *testing.T) {
	st := openTempData(t, map[string][]string{"emp.v2": {"department"}})
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/e/1", Type: "emp.v2", Data: data(t, map[string]any{"department": "support"})}, // 1
		event.Candidate{Source: "s", Subject: "/e/2", Type: "emp.v2", Data: data(t, map[string]any{"department": "sales"})},   // 2
		event.Candidate{Source: "s", Subject: "/e/3", Type: "emp.v2", Data: data(t, map[string]any{"department": "support"})}, // 3
		event.Candidate{Source: "s", Subject: "/e/4", Type: "emp.v2", Data: data(t, map[string]any{"department": "support"})}, // 4
	)

	var ids []string
	err := st.ReadByDataFieldFunc("emp.v2", "department", "support", ReadOptions{Descending: true}, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return true
	})
	if err != nil {
		t.Fatalf("ReadByDataFieldFunc: %v", err)
	}
	if !equalIDs(ids, []string{"4", "3", "1"}) {
		t.Fatalf("data-index support desc = %v, want [4 3 1]", ids)
	}
}
