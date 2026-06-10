package store

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func TestAppendAssignsMonotonicIDs(t *testing.T) {
	st := openTemp(t)

	got, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/a", Type: "t1"},
		{Source: "s", Subject: "/b", Type: "t2"},
		{Source: "s", Subject: "/a", Type: "t3"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, ev := range got {
		if want := strconv.Itoa(i + 1); ev.ID != want {
			t.Fatalf("event[%d].id = %q, want %q", i, ev.ID, want)
		}
		if ev.SpecVersion != event.SpecVersion || ev.Time == "" {
			t.Fatalf("serverfelder fehlen: %+v", ev)
		}
	}
}

func TestReadSubjectFiltersAndOrders(t *testing.T) {
	st := openTemp(t)
	_, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/a", Type: "first"},
		{Source: "s", Subject: "/b", Type: "other"},
		{Source: "s", Subject: "/a", Type: "second"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := st.ReadSubject("/a")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Type != "first" || got[1].Type != "second" {
		t.Fatalf("unerwartetes ergebnis: %+v", got)
	}

	empty, err := st.ReadSubject("/missing")
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("missing subject: len = %d, want 0", len(empty))
	}
}

func TestEmptyAppendNoop(t *testing.T) {
	st := openTemp(t)
	got, err := st.Append(nil)
	if err != nil {
		t.Fatalf("append nil: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
}

// TestPersistenceAcrossReopen stellt sicher, dass Events und die monotone
// Sequenz einen Neustart überstehen.
func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	got, err := st2.ReadSubject("/a")
	if err != nil {
		t.Fatalf("read nach reopen: %v", err)
	}
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("nach reopen: %+v", got)
	}

	// Neue Sequenz setzt fort, vergibt nicht erneut "1".
	more, err := st2.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}})
	if err != nil {
		t.Fatalf("append nach reopen: %v", err)
	}
	if more[0].ID != "2" {
		t.Fatalf("id nach reopen = %q, want %q", more[0].ID, "2")
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
