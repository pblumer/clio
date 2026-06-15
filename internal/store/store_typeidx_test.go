package store

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

func collectByTypes(t *testing.T, st *Store, types []string, opts ReadOptions) []string {
	t.Helper()
	var ids []string
	if err := st.ReadByTypesFunc(types, opts, func(ev event.Event) bool {
		ids = append(ids, ev.ID)
		return true
	}); err != nil {
		t.Fatalf("ReadByTypesFunc: %v", err)
	}
	return ids
}

func TestReadByTypesFunc(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "x"}, // 1
		event.Candidate{Source: "s", Subject: "/b", Type: "y"}, // 2
		event.Candidate{Source: "s", Subject: "/c", Type: "x"}, // 3
		event.Candidate{Source: "s", Subject: "/d", Type: "z"}, // 4
		event.Candidate{Source: "s", Subject: "/e", Type: "y"}, // 5
	)

	// Ein Typ: in globaler Reihenfolge.
	if got := collectByTypes(t, st, []string{"x"}, ReadOptions{}); !equalIDs(got, []string{"1", "3"}) {
		t.Fatalf("typ x = %v, want [1 3]", got)
	}
	// Mehrere Typen: global gemischt, sortiert.
	if got := collectByTypes(t, st, []string{"x", "y"}, ReadOptions{}); !equalIDs(got, []string{"1", "2", "3", "5"}) {
		t.Fatalf("typ x,y = %v, want [1 2 3 5]", got)
	}
	// Bounds.
	if got := collectByTypes(t, st, []string{"y"}, ReadOptions{LowerBound: 3}); !equalIDs(got, []string{"5"}) {
		t.Fatalf("typ y lower=3 = %v, want [5]", got)
	}
	// Unbekannter Typ -> leer.
	if got := collectByTypes(t, st, []string{"nope"}, ReadOptions{}); len(got) != 0 {
		t.Fatalf("unbekannter typ = %v, want leer", got)
	}

	// Früher Abbruch (Limit-Semantik): fn liefert false nach dem ersten Treffer.
	var seen int
	_ = st.ReadByTypesFunc([]string{"x", "y"}, ReadOptions{}, func(event.Event) bool {
		seen++
		return false
	})
	if seen != 1 {
		t.Fatalf("früher Abbruch: %d Aufrufe, want 1", seen)
	}
}

func TestTypeIndexBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backfill.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "x"},
		event.Candidate{Source: "s", Subject: "/b", Type: "y"},
		event.Candidate{Source: "s", Subject: "/c", Type: "x"},
	)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Den Typ-Index leeren — simuliert einen Store aus der Zeit vor dem Index.
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("bolt open: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketTypeIdx); err != nil {
			return err
		}
		_, err := tx.CreateBucket(bucketTypeIdx)
		return err
	}); err != nil {
		t.Fatalf("type-index leeren: %v", err)
	}
	_ = db.Close()

	// Erneutes Öffnen muss den Index aus der Historie nachbauen.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	if got := collectByTypes(t, st2, []string{"x"}, ReadOptions{}); !equalIDs(got, []string{"1", "3"}) {
		t.Fatalf("nach backfill typ x = %v, want [1 3]", got)
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
