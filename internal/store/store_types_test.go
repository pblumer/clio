package store

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func TestEventTypes(t *testing.T) {
	st := openTemp(t)

	// Leerer Store: keine Typen.
	if got, err := st.EventTypes(); err != nil || len(got) != 0 {
		t.Fatalf("leerer store: %v / %+v", err, got)
	}

	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "borrowed"},
		event.Candidate{Source: "s", Subject: "/a", Type: "acquired"},
		event.Candidate{Source: "s", Subject: "/b", Type: "acquired"},
	)

	got, err := st.EventTypes()
	if err != nil {
		t.Fatalf("EventTypes: %v", err)
	}
	// Alphabetisch sortiert (bbolt-Schlüsselordnung): acquired, borrowed.
	want := []TypeInfo{{Type: "acquired", Count: 2}, {Type: "borrowed", Count: 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestEventTypesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "types.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	appendAll(t, st2, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	got, _ := st2.EventTypes()
	if len(got) != 1 || got[0].Type != "t" || got[0].Count != 2 {
		t.Fatalf("nach reopen: %+v (Count soll fortgesetzt werden)", got)
	}
}
