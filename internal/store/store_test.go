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

func TestReadFuncStreamsAndStopsEarly(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t"},
		event.Candidate{Source: "s", Subject: "/b", Type: "t"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t"},
	)

	// Frühabbruch über die Wurzel (recursive "/"): nach 2 Events stoppen.
	var seen int
	err := st.ReadFunc("/", true, ReadOptions{}, func(event.Event) bool {
		seen++
		return seen < 2 // bei 2 false → Abbruch
	})
	if err != nil {
		t.Fatalf("ReadFunc: %v", err)
	}
	if seen != 2 {
		t.Fatalf("seen = %d, want 2 (Frühabbruch)", seen)
	}

	// Nicht-rekursiv über den Subject-Index: ebenfalls abbrechbar.
	seen = 0
	if err := st.ReadFunc("/a", false, ReadOptions{}, func(event.Event) bool {
		seen++
		return false // sofort nach dem ersten stoppen
	}); err != nil {
		t.Fatalf("ReadFunc subject: %v", err)
	}
	if seen != 1 {
		t.Fatalf("seen subject = %d, want 1", seen)
	}

	// Ohne Abbruch liefert ReadFunc dieselbe Menge wie Read (Wrapper-Konsistenz).
	full, err := st.Read("/", true, ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var streamed int
	if err := st.ReadFunc("/", true, ReadOptions{}, func(event.Event) bool { streamed++; return true }); err != nil {
		t.Fatalf("ReadFunc full: %v", err)
	}
	if streamed != len(full) || streamed != 4 {
		t.Fatalf("streamed = %d, Read = %d, want 4", streamed, len(full))
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

func TestReadTypeFilter(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/o/1", Type: "placed"},    // 1
		event.Candidate{Source: "s", Subject: "/o/2", Type: "cancelled"}, // 2
		event.Candidate{Source: "s", Subject: "/o/1", Type: "placed"},    // 3
		event.Candidate{Source: "s", Subject: "/o/3", Type: "shipped"},   // 4
	)

	tests := []struct {
		name      string
		query     string
		recursive bool
		opts      ReadOptions
		wantIDs   []string
	}{
		{"rekursiv ein typ", "/o", true, ReadOptions{Types: []string{"placed"}}, []string{"1", "3"}},
		{"rekursiv mehrere typen", "/o", true, ReadOptions{Types: []string{"placed", "shipped"}}, []string{"1", "3", "4"}},
		{"rekursiv ohne filter", "/o", true, ReadOptions{}, []string{"1", "2", "3", "4"}},
		{"nicht-rekursiv mit typ", "/o/1", false, ReadOptions{Types: []string{"placed"}}, []string{"1", "3"}},
		{"typ + bounds", "/o", true, ReadOptions{Types: []string{"placed"}, LowerBound: 2}, []string{"3"}},
		{"unbekannter typ", "/o", true, ReadOptions{Types: []string{"refunded"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := st.Read(tt.query, tt.recursive, tt.opts)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			ids := idsOf(got)
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

func idsOf(events []event.Event) []string {
	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestReadRecursivePrefixSiblings sichert die index-begrenzte rekursive Lesart
// ab: ein literaler Schlüssel-Prefix darf keine „Geschwister" einschließen, die
// nur zufällig denselben Präfix-String teilen (z. B. /v/1 vs /v/10).
func TestReadRecursivePrefixSiblings(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/v/1", Type: "a"},     // 1 — exakt
		event.Candidate{Source: "s", Subject: "/v/10", Type: "b"},    // 2 — Prefix-Geschwister, NICHT enthalten
		event.Candidate{Source: "s", Subject: "/v/1/sub", Type: "c"}, // 3 — Nachfahre
	)

	got, err := st.Read("/v/1", true, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []string{"1", "3"}
	if ids := idsOf(got); len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("rekursiv /v/1 = %v, want %v (Geschwister /v/10 darf nicht dabei sein)", idsOf(got), want)
	}
}

func TestSubjects(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/books/42", Type: "acquired"},
		event.Candidate{Source: "s", Subject: "/books/99", Type: "acquired"},
		event.Candidate{Source: "s", Subject: "/books/42", Type: "borrowed"},
		event.Candidate{Source: "s", Subject: "/booksstore", Type: "x"}, // Prefix-Geschwister zu /books
		event.Candidate{Source: "s", Subject: "/movies/7", Type: "y"},
	)

	// Ohne prefix: alle Subjects, alphabetisch, mit korrekten Counts.
	all, err := st.Subjects("")
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	want := []SubjectInfo{
		{Subject: "/books/42", Count: 2},
		{Subject: "/books/99", Count: 1},
		{Subject: "/booksstore", Count: 1},
		{Subject: "/movies/7", Count: 1},
	}
	if len(all) != len(want) {
		t.Fatalf("Subjects() = %+v, want %+v", all, want)
	}
	for i, w := range want {
		if all[i] != w {
			t.Fatalf("Subjects()[%d] = %+v, want %+v", i, all[i], w)
		}
	}

	// prefix /books: nur Subjects unter /books — das Prefix-Geschwister
	// /booksstore darf NICHT dabei sein.
	books, err := st.Subjects("/books")
	if err != nil {
		t.Fatalf("Subjects(/books): %v", err)
	}
	bwant := []SubjectInfo{
		{Subject: "/books/42", Count: 2},
		{Subject: "/books/99", Count: 1},
	}
	if len(books) != len(bwant) {
		t.Fatalf("Subjects(/books) = %+v, want %+v (kein /booksstore)", books, bwant)
	}
	for i, w := range bwant {
		if books[i] != w {
			t.Fatalf("Subjects(/books)[%d] = %+v, want %+v", i, books[i], w)
		}
	}

	// prefix ohne Treffer: leer.
	none, err := st.Subjects("/nope")
	if err != nil {
		t.Fatalf("Subjects(/nope): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Subjects(/nope) = %+v, want leer", none)
	}
}

func TestSubjectsEmptyStore(t *testing.T) {
	st := openTemp(t)
	subs, err := st.Subjects("")
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("leerer Store: %+v, want leer", subs)
	}
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

func TestResetClearsEverything(t *testing.T) {
	st := openTemp(t)

	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/books/1", Type: "created", Data: []byte(`{"n":1}`)},
		event.Candidate{Source: "s", Subject: "/books/2", Type: "created", Data: []byte(`{"n":2}`)},
		event.Candidate{Source: "s", Subject: "/users/7", Type: "joined"},
	)
	if err := st.RegisterSchema("created", []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("schema registrieren: %v", err)
	}

	deleted, err := st.Reset()
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	// Alles leer nach dem Reset.
	if n, _ := st.Count(); n != 0 {
		t.Errorf("Count nach reset = %d, want 0", n)
	}
	if subs, _ := st.Subjects(""); len(subs) != 0 {
		t.Errorf("Subjects nach reset = %v, want leer", subs)
	}
	if types, _ := st.EventTypes(); len(types) != 0 {
		t.Errorf("EventTypes nach reset = %v, want leer", types)
	}
	if _, found, _ := st.SchemaFor("created"); found {
		t.Error("Schema für 'created' noch vorhanden, sollte verglüht sein")
	}

	// Neue Sequenz beginnt wieder bei 1, und die (frische) Kette verifiziert.
	got := appendAll(t, st, event.Candidate{Source: "s", Subject: "/fresh", Type: "created"})
	if got[0].ID != "1" {
		t.Errorf("erste ID nach reset = %q, want \"1\"", got[0].ID)
	}
	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify nach reset: %v", err)
	}
	if !res.OK {
		t.Errorf("Verify nach reset nicht ok: %+v", res)
	}
}

func TestResetEmptyStore(t *testing.T) {
	st := openTemp(t)
	deleted, err := st.Reset()
	if err != nil {
		t.Fatalf("reset auf leerem store: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
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

func TestCountByTypes(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "placed"},
		event.Candidate{Source: "s", Subject: "/b", Type: "placed"},
		event.Candidate{Source: "s", Subject: "/c", Type: "cancelled"},
	)
	if n, err := st.CountByTypes([]string{"placed"}); err != nil || n != 2 {
		t.Fatalf("CountByTypes(placed) = %d, %v; want 2", n, err)
	}
	if n, _ := st.CountByTypes([]string{"placed", "cancelled"}); n != 3 {
		t.Fatalf("CountByTypes(placed,cancelled) = %d, want 3", n)
	}
	if n, _ := st.CountByTypes([]string{"unbekannt"}); n != 0 {
		t.Fatalf("CountByTypes(unbekannt) = %d, want 0", n)
	}
	if n, _ := st.CountByTypes(nil); n != 0 {
		t.Fatalf("CountByTypes(nil) = %d, want 0", n)
	}
}

func TestCountSubject(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/books/42", Type: "a"},
		event.Candidate{Source: "s", Subject: "/books/42", Type: "b"},
		event.Candidate{Source: "s", Subject: "/books/99", Type: "a"},
		event.Candidate{Source: "s", Subject: "/booksstore", Type: "x"}, // Prefix-Geschwister
		event.Candidate{Source: "s", Subject: "/movies/7", Type: "y"},
	)
	tests := []struct {
		subject   string
		recursive bool
		want      uint64
	}{
		{"/", true, 5},            // Wurzel = alle Events
		{"/books", true, 3},       // /books/42 (2) + /books/99 (1), ohne /booksstore
		{"/books/42", false, 2},   // exaktes Subject
		{"/books/42", true, 2},    // Blatt, rekursiv = gleich
		{"/booksstore", false, 1}, // Geschwister selbst
		{"/movies", true, 1},      // /movies/7
		{"/nope", true, 0},        // kein Treffer
	}
	for _, tt := range tests {
		got, err := st.CountSubject(tt.subject, tt.recursive)
		if err != nil {
			t.Fatalf("CountSubject(%q,%v): %v", tt.subject, tt.recursive, err)
		}
		if got != tt.want {
			t.Fatalf("CountSubject(%q,%v) = %d, want %d", tt.subject, tt.recursive, got, tt.want)
		}
	}
}

func TestSubjCountBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/books/42", Type: "a"},
		event.Candidate{Source: "s", Subject: "/books/42", Type: "b"},
		event.Candidate{Source: "s", Subject: "/movies/7", Type: "y"},
	)
	// Alt-DB simulieren: subj_count-Bucket leeren (als gäbe es ihn noch nicht).
	if err := st.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketSubjCount); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketSubjCount)
		return err
	}); err != nil {
		t.Fatalf("subj_count leeren: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen -> backfillSubjCount rekonstruiert die Zähler aus der Historie.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if n, _ := st2.CountSubject("/books", true); n != 2 {
		t.Fatalf("nach Backfill CountSubject(/books) = %d, want 2", n)
	}
	if n, _ := st2.CountSubject("/movies/7", false); n != 1 {
		t.Fatalf("nach Backfill CountSubject(/movies/7) = %d, want 1", n)
	}
}

func errorsIsPrecondition(err error) bool {
	return errors.Is(err, ErrPreconditionFailed)
}
