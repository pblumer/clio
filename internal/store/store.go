// Package store implementiert die persistente, append-only Ablage der Events
// auf Basis von bbolt (siehe ADR-006).
//
// Ordnung & Atomarität (ADR-003): bbolt erlaubt zu jedem Zeitpunkt genau eine
// schreibende Transaktion. Diese serialisierte Schreibstelle vergibt die global
// monoton steigenden Event-IDs, prüft die Preconditions und schreibt alle
// Events eines Aufrufs in einer einzigen Transaktion — also alles-oder-nichts.
package store

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// Bucket-Namen. `events` hält die Events nach globaler Sequenz; `subjectIdx`
// bildet Subject → geordnete Sequenznummern ab (für Reads je Subject).
var (
	bucketEvents     = []byte("events")
	bucketSubjectIdx = []byte("subject_idx")
	bucketMeta       = []byte("meta")
	bucketTypes      = []byte("types")
	bucketSchemas    = []byte("schemas")
)

// metaChainHead speichert im meta-Bucket den Hash des zuletzt geschriebenen
// Events (Kopf der Hash-Kette).
var metaChainHead = []byte("chain_head")

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

// ReadOptions filtert ein Read auf einen Bereich globaler Event-IDs und
// optional auf bestimmte Event-Typen. Beide Grenzen sind inklusiv; LowerBound 0
// bedeutet „keine untere Grenze", UpperBound 0 „keine obere Grenze". Ist Types
// leer, werden alle Typen geliefert; sonst nur Events mit passendem `type`.
type ReadOptions struct {
	LowerBound uint64
	UpperBound uint64
	Types      []string
}

// typeSet baut aus Types ein Lookup-Set. nil bedeutet „kein Typ-Filter".
func (o ReadOptions) typeSet() map[string]struct{} {
	if len(o.Types) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(o.Types))
	for _, t := range o.Types {
		set[t] = struct{}{}
	}
	return set
}

// matchType prüft ein Event-Typ gegen das (ggf. leere) Typ-Set.
func matchType(t string, set map[string]struct{}) bool {
	if set == nil {
		return true
	}
	_, ok := set[t]
	return ok
}

// SyncMode steuert die Durability-/Performance-Abwägung beim Schreiben.
type SyncMode int

const (
	// SyncGroup bündelt gleichzeitige Writes per Group Commit in möglichst
	// wenige Transaktionen (ein fsync pro Batch). Volle Durability bei hohem
	// Durchsatz unter Last. Standard.
	SyncGroup SyncMode = iota
	// SyncAlways committet jeden Write einzeln (ein fsync pro Write). Geringste
	// Latenz pro Einzelschreiber, volle Durability, begrenzter Durchsatz.
	SyncAlways
	// SyncOff verzichtet auf fsync. Maximaler Durchsatz, aber bei einem Crash
	// können die zuletzt geschriebenen Events verloren gehen.
	SyncOff
)

// Options konfiguriert den Store.
type Options struct {
	SyncMode SyncMode
	// SigningKey aktiviert (falls gesetzt) die Ed25519-Signatur jedes Events
	// über seinen Hash. nil = nicht signieren.
	SigningKey ed25519.PrivateKey
}

// Store kapselt die bbolt-Datenbank.
type Store struct {
	db       *bolt.DB
	now      func() time.Time
	syncMode SyncMode

	// schemaCache hält kompilierte JSON-Schemas, geschlüsselt nach dem rohen
	// Schema-Inhalt (window-frei: ändert sich der Inhalt, ändert sich der
	// Schlüssel).
	schemaMu    sync.RWMutex
	schemaCache map[string]*jsonschema.Schema

	// signKey signiert Events (optional); verifyKey prüft Signaturen.
	signKey   ed25519.PrivateKey
	verifyKey ed25519.PublicKey
}

// Open öffnet (oder erstellt) die Datenbank unter path mit Standardoptionen
// (Group Commit).
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{SyncMode: SyncGroup})
}

// OpenWithOptions öffnet die Datenbank mit expliziten Optionen und legt die
// nötigen Buckets an.
func OpenWithOptions(path string, opts Options) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("bbolt öffnen: %w", err)
	}

	// SyncOff: bbolt fsync'd nicht mehr beim Commit.
	if opts.SyncMode == SyncOff {
		db.NoSync = true
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketEvents, bucketSubjectIdx, bucketMeta, bucketTypes, bucketSchemas} {
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

	s := &Store{
		db:          db,
		now:         time.Now,
		syncMode:    opts.SyncMode,
		schemaCache: make(map[string]*jsonschema.Schema),
	}
	if opts.SigningKey != nil {
		s.signKey = opts.SigningKey
		s.verifyKey = opts.SigningKey.Public().(ed25519.PublicKey)
	}
	return s, nil
}

// PublicKey liefert den öffentlichen Signaturschlüssel, sofern Signieren aktiv
// ist.
func (s *Store) PublicKey() (ed25519.PublicKey, bool) {
	if s.verifyKey == nil {
		return nil, false
	}
	return s.verifyKey, true
}

// write führt eine Schreibtransaktion gemäß dem konfigurierten SyncMode aus.
// Im Group-Commit-Modus werden gleichzeitige Aufrufe von bbolt coalesced; die
// übergebene Funktion kann dann mehrfach aufgerufen werden und muss daher
// idempotent sein (sie baut ihr Ergebnis bei jedem Lauf frisch auf).
func (s *Store) write(fn func(*bolt.Tx) error) error {
	if s.syncMode == SyncGroup {
		return s.db.Batch(fn)
	}
	return s.db.Update(fn)
}

// Close schließt die Datenbank.
func (s *Store) Close() error {
	return s.db.Close()
}

// Count liefert die Anzahl gespeicherter Events (O(1) über die bbolt-Sequenz).
func (s *Store) Count() (uint64, error) {
	var n uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketEvents).Sequence()
		return nil
	})
	return n, err
}

// TypeInfo beschreibt einen bisher geschriebenen Event-Typ.
type TypeInfo struct {
	Type      string `json:"type"`
	Count     uint64 `json:"count"`
	HasSchema bool   `json:"hasSchema"`
}

// EventTypes liefert alle bisher geschriebenen Event-Typen in alphabetischer
// Reihenfolge (bbolt-Schlüsselordnung) samt Anzahl und ob ein Schema registriert
// ist.
func (s *Store) EventTypes() ([]TypeInfo, error) {
	var out []TypeInfo
	err := s.db.View(func(tx *bolt.Tx) error {
		schemas := tx.Bucket(bucketSchemas)
		c := tx.Bucket(bucketTypes).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			out = append(out, TypeInfo{
				Type:      string(k),
				Count:     binary.BigEndian.Uint64(v),
				HasSchema: schemas.Get(k) != nil,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// incrTypeCount erhöht den Zähler eines Event-Typs im types-Bucket.
func incrTypeCount(types *bolt.Bucket, t string) error {
	var cnt uint64
	if v := types.Get([]byte(t)); len(v) == 8 {
		cnt = binary.BigEndian.Uint64(v)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], cnt+1)
	return types.Put([]byte(t), buf[:])
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
	var events []event.Event

	err := s.write(func(tx *bolt.Tx) error {
		// Bei Group-Commit-Retries kann diese Funktion erneut laufen — Ergebnis
		// daher bei jedem Lauf frisch aufbauen.
		events = make([]event.Event, 0, len(candidates))

		if err := checkPreconditions(tx, preconditions); err != nil {
			return err
		}

		evts := tx.Bucket(bucketEvents)
		idx := tx.Bucket(bucketSubjectIdx)
		meta := tx.Bucket(bucketMeta)
		types := tx.Bucket(bucketTypes)

		// Kopf der Hash-Kette lesen (Genesis, falls leer). Innerhalb einer
		// (Group-Commit-)Transaktion sehen Folgeschreiber den aktualisierten
		// Kopf, sodass die Kette auch über coalesced Aufrufe korrekt bleibt.
		head := event.GenesisHash
		if h := meta.Get(metaChainHead); len(h) > 0 {
			head = string(h)
		}

		for _, c := range candidates {
			// Gegen ein ggf. registriertes Schema validieren (vor dem Schreiben).
			if err := s.validateAgainstSchema(tx, c.Type, c.Data); err != nil {
				return err
			}

			seq, err := evts.NextSequence()
			if err != nil {
				return err
			}

			// Data kanonisch (kompakt) speichern, damit der Hash reproduzierbar ist.
			data, dct, err := canonicalData(c.Data)
			if err != nil {
				return err
			}

			ev := event.Event{
				SpecVersion:     event.SpecVersion,
				ID:              strconv.FormatUint(seq, 10),
				Time:            now,
				Source:          c.Source,
				Subject:         c.Subject,
				Type:            c.Type,
				DataContentType: dct,
				Data:            data,
				PredecessorHash: head,
			}
			ev.Hash = event.ComputeHash(ev)
			head = ev.Hash

			// Optional: Event über seinen Hash signieren (Authentizität).
			if s.signKey != nil {
				sig, err := signHash(s.signKey, ev.Hash)
				if err != nil {
					return err
				}
				ev.Signature = &sig
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
			if err := incrTypeCount(types, c.Type); err != nil {
				return err
			}

			events = append(events, ev)
		}

		// Neuen Ketten-Kopf persistieren.
		return meta.Put(metaChainHead, []byte(head))
	})
	if err != nil {
		if errors.Is(err, ErrPreconditionFailed) || errors.Is(err, ErrSchemaValidation) {
			return nil, err
		}
		return nil, fmt.Errorf("events schreiben: %w", err)
	}

	return events, nil
}

// canonicalData bringt die Nutzdaten in kompakte (kanonische) Form und liefert
// den passenden datacontenttype. Leere Daten ergeben leeren content-type.
func canonicalData(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		return nil, "", nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, "", fmt.Errorf("data kompaktieren: %w", err)
	}
	return buf.Bytes(), event.JSONContentType, nil
}

// VerifyResult ist das Ergebnis einer Integritätsprüfung der Hash-Kette.
type VerifyResult struct {
	OK       bool   `json:"ok"`
	Count    uint64 `json:"count"`              // geprüfte Events
	Head     string `json:"head"`               // Hash des letzten Events (oder Genesis)
	BrokenAt string `json:"brokenAt,omitempty"` // Event-ID, an der die Kette bricht
	Reason   string `json:"reason,omitempty"`
}

// Verify rechnet die gesamte Hash-Kette in globaler Reihenfolge nach und meldet
// die erste Bruchstelle. Eine intakte Kette beweist, dass kein historisches
// Event nachträglich verändert wurde (Tamper-Evidence).
func (s *Store) Verify() (VerifyResult, error) {
	res := VerifyResult{OK: true, Head: event.GenesisHash}
	prev := event.GenesisHash

	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketEvents).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var ev event.Event
			if err := json.Unmarshal(v, &ev); err != nil {
				return fmt.Errorf("event dekodieren: %w", err)
			}
			res.Count++

			if ev.PredecessorHash != prev {
				res.OK = false
				res.BrokenAt = ev.ID
				res.Reason = "predecessorhash passt nicht zum Vorgänger"
				return nil
			}
			if want := event.ComputeHash(ev); ev.Hash != want {
				res.OK = false
				res.BrokenAt = ev.ID
				res.Reason = "hash stimmt nicht mit dem Inhalt überein"
				return nil
			}
			// Signatur prüfen, sofern vorhanden und ein Schlüssel konfiguriert ist.
			if s.verifyKey != nil && ev.Signature != nil {
				if err := verifySignature(s.verifyKey, ev.Hash, *ev.Signature); err != nil {
					res.OK = false
					res.BrokenAt = ev.ID
					res.Reason = "signatur ungültig: " + err.Error()
					return nil
				}
			}
			prev = ev.Hash
		}

		res.Head = prev
		// Gespeicherter Ketten-Kopf muss zum letzten Event passen.
		storedHead := event.GenesisHash
		if h := tx.Bucket(bucketMeta).Get(metaChainHead); len(h) > 0 {
			storedHead = string(h)
		}
		if storedHead != prev {
			res.OK = false
			res.Reason = "gespeicherter Ketten-Kopf passt nicht zum letzten Event"
		}
		return nil
	})
	if err != nil {
		return VerifyResult{}, err
	}
	return res, nil
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
	types := opts.typeSet()

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
		if !matchType(ev.Type, types) {
			continue
		}
		*out = append(*out, ev)
	}
	return nil
}

func readRecursive(tx *bolt.Tx, query string, opts ReadOptions, out *[]event.Event) error {
	cur := tx.Bucket(bucketEvents).Cursor()
	types := opts.typeSet()

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
		if MatchSubject(ev.Subject, query, true) && matchType(ev.Type, types) {
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
