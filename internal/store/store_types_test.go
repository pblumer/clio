package store

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

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

// TestEventTypesBackfill simuliert einen Store, der bereits Events enthielt,
// bevor der types-Bucket eingeführt wurde: der Zähler-Bucket wird geleert und
// der Store erneut geöffnet. Beim Öffnen müssen die Typ-Zähler aus den
// vorhandenen Events rekonstruiert werden (sonst liefert read-event-types nichts,
// obwohl Events vorhanden sind).
func TestEventTypesBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backfill.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "borrowed"},
		event.Candidate{Source: "s", Subject: "/a", Type: "acquired"},
		event.Candidate{Source: "s", Subject: "/b", Type: "acquired"},
	)

	// types-Bucket leeren = Zustand vor Einführung des Zähler-Buckets.
	if err := st.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketTypes); err != nil {
			return err
		}
		_, err := tx.CreateBucket(bucketTypes)
		return err
	}); err != nil {
		t.Fatalf("types leeren: %v", err)
	}
	if got, _ := st.EventTypes(); len(got) != 0 {
		t.Fatalf("vorbedingung: types-bucket sollte leer sein, war %+v", got)
	}
	_ = st.Close()

	// Reopen muss die Zähler aus den Events rekonstruieren.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	got, err := st2.EventTypes()
	if err != nil {
		t.Fatalf("EventTypes: %v", err)
	}
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
