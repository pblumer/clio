// Package store implementiert die persistente, append-only Ablage der Events
// auf Basis von bbolt (siehe ADR-006).
//
// Ordnung & Atomarität (ADR-003): bbolt erlaubt zu jedem Zeitpunkt genau eine
// schreibende Transaktion. Diese serialisierte Schreibstelle vergibt die global
// monoton steigenden Event-IDs, prüft die Preconditions und schreibt alle
// Events eines Aufrufs in einer einzigen Transaktion — also alles-oder-nichts.
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// Bucket-Namen. `events` hält die Events nach globaler Sequenz; `subjectIdx`
// bildet Subject → geordnete Sequenznummern ab (für Reads je Subject).
var (
	bucketEvents     = []byte("events")
	bucketSubjectIdx = []byte("subject_idx")
)

// subjectSep trennt im Subject-Index das Subject von der Sequenznummer.
// 0x00 kommt in Subject-Pfaden nicht vor und ist daher als Separator sicher.
const subjectSep = 0x00

// ErrPreconditionFailed wird zurückgegeben, wenn eine Precondition beim
// Schreiben nicht erfüllt ist (Optimistic Concurrency). Aufrufer können dies
// per errors.Is erkennen und z. B. auf HTTP 409 abbilden.
var ErrPreconditionFailed = errors.New("precondition nicht erfüllt")

// Precondition-Typen (siehe ADR / API-Kontrakt).
const (
	PreconditionSubjectPristine  = "isSubjectPristine"
	PreconditionSubjectOnEventID = "isSubjectOnEventId"
)

// Precondition ist eine vor dem Write zu erfüllende Bedingung. Subject ist für
// alle Typen relevant; EventID nur für isSubjectOnEventId.
type Precondition struct {
	Type    string
	Subject string
	EventID string
}

// ReadOptions filtert ein ReadSubject auf einen Bereich globaler Event-IDs.
// Beide Grenzen sind inklusiv. LowerBound 0 bedeutet „keine untere Grenze";
// UpperBound 0 bedeutet „keine obere Grenze".
type ReadOptions struct {
	LowerBound uint64
	UpperBound uint64
}

// Store kapselt die bbolt-Datenbank.
type Store struct {
	db  *bolt.DB
	now func() time.Time
}

// Open öffnet (oder erstellt) die Datenbank unter path und legt die nötigen
// Buckets an.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("bbolt öffnen: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketEvents, bucketSubjectIdx} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("buckets anlegen: %w", err)
	}

	return &Store{db: db, now: time.Now}, nil
}

// Close schließt die Datenbank.
func (s *Store) Close() error {
	return s.db.Close()
}

// Append prüft die Preconditions und speichert anschließend eine oder mehrere
// Candidates atomar (alles-oder-nichts). Schlägt eine Precondition fehl, wird
// nichts geschrieben und ein in ErrPreconditionFailed gehüllter Fehler
// zurückgegeben. Bei leerer Eingabe wird ein leeres Ergebnis zurückgegeben.
func (s *Store) Append(candidates []event.Candidate, preconditions []Precondition) ([]event.Event, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	now := s.now().UTC().Format(time.RFC3339Nano)
	events := make([]event.Event, 0, len(candidates))

	err := s.db.Update(func(tx *bolt.Tx) error {
		if err := checkPreconditions(tx, preconditions); err != nil {
			return err
		}

		evts := tx.Bucket(bucketEvents)
		idx := tx.Bucket(bucketSubjectIdx)

		for _, c := range candidates {
			seq, err := evts.NextSequence()
			if err != nil {
				return err
			}

			ev := event.Event{
				SpecVersion: event.SpecVersion,
				ID:          strconv.FormatUint(seq, 10),
				Time:        now,
				Source:      c.Source,
				Subject:     c.Subject,
				Type:        c.Type,
				Data:        c.Data,
			}

			payload, err := json.Marshal(ev)
			if err != nil {
				return err
			}

			key := seqKey(seq)
			if err := evts.Put(key, payload); err != nil {
				return err
			}
			if err := idx.Put(subjectKey(c.Subject, seq), key); err != nil {
				return err
			}

			events = append(events, ev)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrPreconditionFailed) {
			return nil, err
		}
		return nil, fmt.Errorf("events schreiben: %w", err)
	}

	return events, nil
}

// checkPreconditions wertet alle Preconditions innerhalb der Schreibtransaktion
// aus, damit Prüfung und Write atomar sind.
func checkPreconditions(tx *bolt.Tx, preconditions []Precondition) error {
	idx := tx.Bucket(bucketSubjectIdx)

	for _, p := range preconditions {
		last, exists := lastSeq(idx, p.Subject)

		switch p.Type {
		case PreconditionSubjectPristine:
			if exists {
				return fmt.Errorf("%w: subject %q ist nicht leer", ErrPreconditionFailed, p.Subject)
			}
		case PreconditionSubjectOnEventID:
			want, err := strconv.ParseUint(p.EventID, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: ungültige eventId %q", ErrPreconditionFailed, p.EventID)
			}
			if !exists {
				return fmt.Errorf("%w: subject %q ist leer, erwartet eventId %s", ErrPreconditionFailed, p.Subject, p.EventID)
			}
			if last != want {
				return fmt.Errorf("%w: subject %q steht auf eventId %d, erwartet %d", ErrPreconditionFailed, p.Subject, last, want)
			}
		default:
			return fmt.Errorf("%w: unbekannter precondition-typ %q", ErrPreconditionFailed, p.Type)
		}
	}
	return nil
}

// lastSeq liefert die Sequenznummer des letzten Events eines Subjects und ob
// der Stream überhaupt Events enthält.
func lastSeq(idx *bolt.Bucket, subject string) (uint64, bool) {
	prefix := append([]byte(subject), subjectSep)
	cur := idx.Cursor()

	// Auf den ersten Schlüssel hinter dem Prefix springen und einen Schritt
	// zurück: das ist der letzte Eintrag des Subjects (sofern vorhanden).
	end := append([]byte(subject), subjectSep+1)
	k, _ := cur.Seek(end)
	if k == nil {
		k, _ = cur.Last()
	} else {
		k, _ = cur.Prev()
	}
	if k == nil || !hasPrefix(k, prefix) {
		return 0, false
	}
	return binary.BigEndian.Uint64(k[len(prefix):]), true
}

// MatchSubject prüft, ob ein Event-Subject zu einer Abfrage passt. Ohne
// recursive ist nur exakte Gleichheit ein Treffer; mit recursive zählen das
// Subject selbst und alle untergeordneten Pfade (Prefix an der "/"-Grenze).
// Die Wurzel "/" matcht rekursiv alles.
func MatchSubject(subject, query string, recursive bool) bool {
	if !recursive {
		return subject == query
	}
	if query == "/" || subject == query {
		return true
	}
	return strings.HasPrefix(subject, strings.TrimSuffix(query, "/")+"/")
}

// ReadSubject liefert die Events genau eines Subjects (nicht rekursiv).
func (s *Store) ReadSubject(subject string, opts ReadOptions) ([]event.Event, error) {
	return s.Read(subject, false, opts)
}

// Read liefert Events in globaler Schreibreihenfolge, gefiltert auf das (ggf.
// rekursive) Subject und den durch opts beschriebenen ID-Bereich.
//
// Nicht-rekursiv wird über den Subject-Index gelesen (nur die Events des
// Subjects). Rekursiv wird der nach globaler Sequenz geordnete events-Bucket
// durchlaufen und per MatchSubject gefiltert — so bleibt die globale Ordnung
// auch über mehrere Subjects hinweg erhalten.
func (s *Store) Read(query string, recursive bool, opts ReadOptions) ([]event.Event, error) {
	var events []event.Event

	err := s.db.View(func(tx *bolt.Tx) error {
		if recursive {
			return readRecursive(tx, query, opts, &events)
		}
		return readSubjectIndex(tx, query, opts, &events)
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func readSubjectIndex(tx *bolt.Tx, subject string, opts ReadOptions, out *[]event.Event) error {
	evts := tx.Bucket(bucketEvents)
	cur := tx.Bucket(bucketSubjectIdx).Cursor()
	prefix := append([]byte(subject), subjectSep)

	for k, evKey := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, evKey = cur.Next() {
		seq := binary.BigEndian.Uint64(evKey)
		if !inBounds(seq, opts) {
			continue
		}
		raw := evts.Get(evKey)
		if raw == nil {
			return fmt.Errorf("inkonsistenter index: event %x fehlt", evKey)
		}
		var ev event.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return fmt.Errorf("event dekodieren: %w", err)
		}
		*out = append(*out, ev)
	}
	return nil
}

func readRecursive(tx *bolt.Tx, query string, opts ReadOptions, out *[]event.Event) error {
	cur := tx.Bucket(bucketEvents).Cursor()

	var k, v []byte
	if opts.LowerBound != 0 {
		k, v = cur.Seek(seqKey(opts.LowerBound))
	} else {
		k, v = cur.First()
	}

	for ; k != nil; k, v = cur.Next() {
		seq := binary.BigEndian.Uint64(k)
		if opts.UpperBound != 0 && seq > opts.UpperBound {
			break
		}
		var ev event.Event
		if err := json.Unmarshal(v, &ev); err != nil {
			return fmt.Errorf("event dekodieren: %w", err)
		}
		if MatchSubject(ev.Subject, query, true) {
			*out = append(*out, ev)
		}
	}
	return nil
}

// inBounds prüft, ob eine Sequenz innerhalb der (inklusiven) ID-Grenzen liegt.
func inBounds(seq uint64, opts ReadOptions) bool {
	if opts.LowerBound != 0 && seq < opts.LowerBound {
		return false
	}
	if opts.UpperBound != 0 && seq > opts.UpperBound {
		return false
	}
	return true
}

// seqKey kodiert eine Sequenznummer als 8-Byte-Big-Endian, sodass die
// bbolt-Schlüsselordnung der numerischen Reihenfolge entspricht.
func seqKey(seq uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, seq)
	return b
}

// subjectKey bildet den Index-Schlüssel subject + sep + seq.
func subjectKey(subject string, seq uint64) []byte {
	key := make([]byte, 0, len(subject)+1+8)
	key = append(key, subject...)
	key = append(key, subjectSep)
	return append(key, seqKey(seq)...)
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
