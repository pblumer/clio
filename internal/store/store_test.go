package store

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// appendAll ist ein Test-Helfer für Writes ohne Preconditions.
func appendAll(t *testing.T, st *Store, cands ...event.Candidate) []event.Event {
	t.Helper()
	got, err := st.Append(cands, nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return got
}

func TestAppendAssignsMonotonicIDs(t *testing.T) {
	st := openTemp(t)

	got := appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t1"},
		event.Candidate{Source: "s", Subject: "/b", Type: "t2"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t3"},
	)
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
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "first"},
		event.Candidate{Source: "s", Subject: "/b", Type: "other"},
		event.Candidate{Source: "s", Subject: "/a", Type: "second"},
	)

	got, err := st.ReadSubject("/a", ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Type != "first" || got[1].Type != "second" {
		t.Fatalf("unerwartetes ergebnis: %+v", got)
	}

	empty, err := st.ReadSubject("/missing", ReadOptions{})
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("missing subject: len = %d, want 0", len(empty))
	}
}

func TestReadSubjectBounds(t *testing.T) {
	st := openTemp(t)
	// IDs 1..5 alle in /s.
	for i := 0; i < 5; i++ {
		appendAll(t, st, event.Candidate{Source: "s", Subject: "/s", Type: "t"})
	}

	tests := []struct {
		name    string
		opts    ReadOptions
		wantIDs []string
	}{
		{"ohne grenzen", ReadOptions{}, []string{"1", "2", "3", "4", "5"}},
		{"nur lower", ReadOptions{LowerBound: 3}, []string{"3", "4", "5"}},
		{"nur upper", ReadOptions{UpperBound: 2}, []string{"1", "2"}},
		{"beide inklusiv", ReadOptions{LowerBound: 2, UpperBound: 4}, []string{"2", "3", "4"}},
		{"exakt einer", ReadOptions{LowerBound: 3, UpperBound: 3}, []string{"3"}},
		{"leerer bereich", ReadOptions{LowerBound: 4, UpperBound: 2}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := st.ReadSubject("/s", tt.opts)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var ids []string
			for _, ev := range got {
				ids = append(ids, ev.ID)
			}
			if len(ids) != len(tt.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tt.wantIDs)
			}
			for i := range ids {
				if ids[i] != tt.wantIDs[i] {
					t.Fatalf("ids = %v, want %v", ids, tt.wantIDs)
				}
			}
		})
	}
}

func TestMatchSubject(t *testing.T) {
	tests := []struct {
		subject, query string
		recursive      bool
		want           bool
	}{
		{"/books/42", "/books/42", false, true},
		{"/books/42", "/books", false, false},
		{"/books", "/books", true, true},
		{"/books/42", "/books", true, true},
		{"/books/42/pages", "/books", true, true},
		{"/booksXYZ", "/books", true, false}, // kein "/"-Grenztreffer
		{"/anything", "/", true, true},       // Wurzel rekursiv = alles
		{"/anything", "/", false, false},     // Wurzel nicht-rekursiv ≠ alles
	}
	for _, tt := range tests {
		if got := MatchSubject(tt.subject, tt.query, tt.recursive); got != tt.want {
			t.Errorf("MatchSubject(%q, %q, %v) = %v, want %v", tt.subject, tt.query, tt.recursive, got, tt.want)
		}
	}
}

func TestReadRecursiveGlobalOrder(t *testing.T) {
	st := openTemp(t)
	// Abwechselnd in verschachtelte Subjects schreiben -> IDs 1..4 global.
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/books/42", Type: "a"},     // 1
		event.Candidate{Source: "s", Subject: "/users/7", Type: "b"},      // 2
		event.Candidate{Source: "s", Subject: "/books/99", Type: "c"},     // 3
		event.Candidate{Source: "s", Subject: "/books/42/log", Type: "d"}, // 4
	)

	// Rekursiv ab /books: 1,3,4 in globaler Reihenfolge (nicht /users/7).
	got, err := st.Read("/books", true, ReadOptions{})
	if err != nil {
		t.Fatalf("read recursive: %v", err)
	}
	wantIDs := []string{"1", "3", "4"}
	if len(got) != len(wantIDs) {
		t.Fatalf("ids = %v, want %v", idsOf(got), wantIDs)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("ids = %v, want %v (globale ordnung verletzt)", idsOf(got), wantIDs)
		}
	}

	// Wurzel rekursiv = alle 4 in globaler Reihenfolge.
	all, _ := st.Read("/", true, ReadOptions{})
	if len(all) != 4 || all[0].ID != "1" || all[3].ID != "4" {
		t.Fatalf("root recursive: %v", idsOf(all))
	}

	// Rekursiv mit Bounds.
	bounded, _ := st.Read("/books", true, ReadOptions{LowerBound: 3, UpperBound: 4})
	if len(bounded) != 2 || bounded[0].ID != "3" || bounded[1].ID != "4" {
		t.Fatalf("recursive bounds: %v", idsOf(bounded))
	}

	// Nicht-rekursiv ab /books matcht keinen exakten Stream -> leer.
	none, _ := st.Read("/books", false, ReadOptions{})
	if len(none) != 0 {
		t.Fatalf("nicht-rekursiv /books: %v", idsOf(none))
	}
}

func idsOf(events []event.Event) []string {
	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestPreconditionSubjectPristine(t *testing.T) {
	st := openTemp(t)

	// Auf leeren Stream schreiben: erfüllt.
	pre := []Precondition{{Type: PreconditionSubjectPristine, Subject: "/x"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t"}}, pre); err != nil {
		t.Fatalf("pristine auf leerem stream: %v", err)
	}

	// Jetzt ist /x nicht mehr leer: zweiter Write muss scheitern.
	_, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t2"}}, pre)
	if !errorsIsPrecondition(err) {
		t.Fatalf("erwartete ErrPreconditionFailed, bekam %v", err)
	}

	// Nichts darf aus dem fehlgeschlagenen Write geschrieben worden sein.
	got, _ := st.ReadSubject("/x", ReadOptions{})
	if len(got) != 1 {
		t.Fatalf("nach fehlgeschlagenem write: %d events, want 1", len(got))
	}
}

func TestPreconditionSubjectOnEventID(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/x", Type: "t1"}) // ID 1

	// Korrekte erwartete letzte ID: erfüllt.
	ok := []Precondition{{Type: PreconditionSubjectOnEventID, Subject: "/x", EventID: "1"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t2"}}, ok); err != nil {
		t.Fatalf("onEventId korrekt: %v", err)
	}

	// Veraltete erwartete ID (jetzt steht /x auf 2): muss scheitern.
	stale := []Precondition{{Type: PreconditionSubjectOnEventID, Subject: "/x", EventID: "1"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t3"}}, stale); !errorsIsPrecondition(err) {
		t.Fatalf("erwartete ErrPreconditionFailed bei veralteter id, bekam %v", err)
	}

	// onEventId gegen leeren Stream: muss scheitern.
	onEmpty := []Precondition{{Type: PreconditionSubjectOnEventID, Subject: "/leer", EventID: "1"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/leer", Type: "t"}}, onEmpty); !errorsIsPrecondition(err) {
		t.Fatalf("erwartete ErrPreconditionFailed bei leerem stream, bekam %v", err)
	}
}

func TestPreconditionUnknownAndBadID(t *testing.T) {
	st := openTemp(t)

	unknown := []Precondition{{Type: "isMagic", Subject: "/x"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t"}}, unknown); !errorsIsPrecondition(err) {
		t.Fatalf("erwartete ErrPreconditionFailed bei unbekanntem typ, bekam %v", err)
	}

	badID := []Precondition{{Type: PreconditionSubjectOnEventID, Subject: "/x", EventID: "nope"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "t"}}, badID); !errorsIsPrecondition(err) {
		t.Fatalf("erwartete ErrPreconditionFailed bei kaputter id, bekam %v", err)
	}
}

func TestEmptyAppendNoop(t *testing.T) {
	st := openTemp(t)
	got, err := st.Append(nil, nil)
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
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}, nil); err != nil {
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

	got, err := st2.ReadSubject("/a", ReadOptions{})
	if err != nil {
		t.Fatalf("read nach reopen: %v", err)
	}
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("nach reopen: %+v", got)
	}

	// Neue Sequenz setzt fort, vergibt nicht erneut "1".
	more, err := st2.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}, nil)
	if err != nil {
		t.Fatalf("append nach reopen: %v", err)
	}
	if more[0].ID != "2" {
		t.Fatalf("id nach reopen = %q, want %q", more[0].ID, "2")
	}
}

// TestOpenError: ein Verzeichnis statt einer Datei lässt bbolt scheitern.
func TestOpenError(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("erwartete fehler beim öffnen eines verzeichnisses, bekam nil")
	}
}

// TestAppendMarshalError: ungültiges Data-JSON lässt das Marshalling des Events
// in der Transaktion scheitern (Validierung passiert eine Schicht höher).
func TestAppendMarshalError(t *testing.T) {
	st := openTemp(t)
	_, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/a", Type: "t", Data: []byte("{kaputt")},
	}, nil)
	if err == nil {
		t.Fatal("erwartete marshal-fehler bei ungültigem data, bekam nil")
	}
	// Nichts darf geschrieben worden sein (Transaktion rollt zurück).
	got, rerr := st.ReadSubject("/a", ReadOptions{})
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if len(got) != 0 {
		t.Fatalf("nach fehlgeschlagenem append: %d events, want 0", len(got))
	}
}

// TestReadSubjectInconsistentIndex: ein Index-Eintrag ohne zugehöriges Event
// muss einen Fehler liefern statt still zu schlucken.
func TestReadSubjectInconsistentIndex(t *testing.T) {
	st := openTemp(t)
	const seq = 999
	err := st.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSubjectIdx).Put(subjectKey("/x", seq), seqKey(seq))
	})
	if err != nil {
		t.Fatalf("index präparieren: %v", err)
	}

	if _, err := st.ReadSubject("/x", ReadOptions{}); err == nil {
		t.Fatal("erwartete fehler bei inkonsistentem index, bekam nil")
	}
}

// TestReadSubjectDecodeError: ein kaputt gespeichertes Event muss beim Lesen
// einen Fehler liefern.
func TestReadSubjectDecodeError(t *testing.T) {
	st := openTemp(t)
	const seq = 500
	err := st.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketEvents).Put(seqKey(seq), []byte("kein json")); err != nil {
			return err
		}
		return tx.Bucket(bucketSubjectIdx).Put(subjectKey("/y", seq), seqKey(seq))
	})
	if err != nil {
		t.Fatalf("event präparieren: %v", err)
	}

	if _, err := st.ReadSubject("/y", ReadOptions{}); err == nil {
		t.Fatal("erwartete decode-fehler, bekam nil")
	}
}

// TestReadRecursiveDecodeError: kaputtes Event muss auch beim rekursiven Lesen
// einen Fehler liefern.
func TestReadRecursiveDecodeError(t *testing.T) {
	st := openTemp(t)
	err := st.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketEvents).Put(seqKey(1), []byte("kein json"))
	})
	if err != nil {
		t.Fatalf("event präparieren: %v", err)
	}
	if _, err := st.Read("/", true, ReadOptions{}); err == nil {
		t.Fatal("erwartete decode-fehler beim rekursiven lesen, bekam nil")
	}
}

func TestHasPrefix(t *testing.T) {
	tests := []struct {
		b, prefix string
		want      bool
	}{
		{"/books/42", "/books", true},
		{"/books", "/books", true},
		{"/bo", "/books", false}, // kürzer als prefix
		{"/cards", "/books", false},
		{"", "", true},
	}
	for _, tt := range tests {
		if got := hasPrefix([]byte(tt.b), []byte(tt.prefix)); got != tt.want {
			t.Errorf("hasPrefix(%q, %q) = %v, want %v", tt.b, tt.prefix, got, tt.want)
		}
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

func errorsIsPrecondition(err error) bool {
	return errors.Is(err, ErrPreconditionFailed)
}
