// Package store implementiert die persistente, append-only Ablage der Events
// auf Basis von bbolt (siehe ADR-006).
//
// Ordnung & Atomarität (ADR-003): bbolt erlaubt zu jedem Zeitpunkt genau eine
// schreibende Transaktion. Diese serialisierte Schreibstelle vergibt die global
// monoton steigenden Event-IDs und schreibt alle Events eines Aufrufs in einer
// einzigen Transaktion — also alles-oder-nichts.
package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
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

// Append speichert eine oder mehrere Candidates atomar (alles-oder-nichts) und
// liefert die fertig ergänzten Events in Schreibreihenfolge zurück. Bei leerer
// Eingabe wird ein leeres Ergebnis ohne Transaktion zurückgegeben.
func (s *Store) Append(candidates []event.Candidate) ([]event.Event, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	now := s.now().UTC().Format(time.RFC3339Nano)
	events := make([]event.Event, 0, len(candidates))

	err := s.db.Update(func(tx *bolt.Tx) error {
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
		return nil, fmt.Errorf("events schreiben: %w", err)
	}

	return events, nil
}

// ReadSubject liefert alle Events eines Subjects in Schreibreihenfolge.
func (s *Store) ReadSubject(subject string) ([]event.Event, error) {
	var events []event.Event

	prefix := append([]byte(subject), subjectSep)

	err := s.db.View(func(tx *bolt.Tx) error {
		evts := tx.Bucket(bucketEvents)
		cur := tx.Bucket(bucketSubjectIdx).Cursor()

		for k, seqKey := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, seqKey = cur.Next() {
			raw := evts.Get(seqKey)
			if raw == nil {
				return fmt.Errorf("inkonsistenter index: event %x fehlt", seqKey)
			}
			var ev event.Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				return fmt.Errorf("event dekodieren: %w", err)
			}
			events = append(events, ev)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return events, nil
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
