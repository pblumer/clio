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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/query"
)

// Bucket-Namen. `events` hält die Events nach globaler Sequenz; `subjectIdx`
// bildet Subject → geordnete Sequenznummern ab (für Reads je Subject).
var (
	bucketEvents     = []byte("events")
	bucketSubjectIdx = []byte("subject_idx")
	bucketTypeIdx    = []byte("type_idx")
	bucketMeta       = []byte("meta")
	bucketTypes      = []byte("types")
	bucketSubjCount  = []byte("subj_count")
	bucketSchemas    = []byte("schemas")
	// bucketDataIdx ist der deklarative Sekundärindex auf `event.data`-Felder
	// (ADR-029): Schlüssel (typ, feld, wert, seq) → Event-Sequenz. Nur explizit
	// per Options.DataIndexFields deklarierte (typ,feld)-Paare werden gepflegt.
	bucketDataIdx = []byte("data_idx")
	// bucketAuthKeys hält den Schlüsselbund (kid → JSON-Key, ADR-025). Bewusst
	// getrennt vom Event-Strom: es sind mutable Steuerungsdaten und vom Reset
	// (ADR-022) ausgenommen — siehe Reset().
	bucketAuthKeys = []byte("auth_keys")
	// bucketAuditLog hält das append-only Audit-Log administrativer Aktionen
	// (seq → JSON-AuditEntry, ADR-031). Eigener Bucket, getrennt vom Event-Strom,
	// damit Audit-Einträge Fach-Events nicht stören und nicht über die Write-API
	// erreichbar sind. Wie auth_keys vom Reset (ADR-022) ausgenommen.
	bucketAuditLog = []byte("audit_log")
)

// metaChainHead speichert im meta-Bucket den Hash des zuletzt geschriebenen
// Events (Kopf der Hash-Kette).
var metaChainHead = []byte("chain_head")

// metaDataIdxCovered speichert im meta-Bucket die Menge der (typ,feld)-Paare,
// für die der Daten-Index (ADR-029) bereits über die gesamte Historie aufgebaut
// wurde (JSON-Array aus "typ\x00feld"-Strings). So wird ein neu deklariertes
// Feld beim nächsten Öffnen einmalig nachindiziert, ohne bereits abgedeckte
// Felder erneut zu scannen.
var metaDataIdxCovered = []byte("data_idx_covered")

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
	// Query-Preconditions (ADR-017): erfüllt, wenn eine CEL-Abfrage über einen
	// Scope kein bzw. mindestens ein Treffer-Event liefert.
	PreconditionQueryResultEmpty    = "isQueryResultEmpty"
	PreconditionQueryResultNonEmpty = "isQueryResultNonEmpty"
)

// Precondition ist eine vor dem Write zu erfüllende Bedingung. Subject ist für
// alle Typen relevant; EventID nur für isSubjectOnEventId; Recursive und
// Predicate nur für die Query-Preconditions (Predicate nil = nur Scope-Existenz).
type Precondition struct {
	Type      string
	Subject   string
	EventID   string
	Recursive bool
	Predicate *query.Predicate
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
	// Compress aktiviert die transparente DEFLATE-Kompression der gespeicherten
	// Event-Werte (ADR-024). Aus = Werte werden roh als JSON abgelegt (Default,
	// byte-identisch zum bisherigen Verhalten). An = neue Werte werden komprimiert;
	// das Lesen erkennt beide Formen, sodass gemischte Datenbanken funktionieren.
	Compress bool

	// InitialMmapSize dimensioniert die bbolt-Mmap (in Bytes) vorab. bbolt mappt
	// die Datei beim Überschreiten der Mmap-Grenze neu (allocate → db.mmap), hält
	// dabei kurz den mmaplock exklusiv und wartet auf das Freigeben aller
	// Lese-Transaktionen — unter Leselast erzeugt das Schreib-Latenzspitzen. Eine
	// vorab große Mmap verschiebt diese Remaps weit nach hinten. Zusätzlich wird
	// die Datei real auf diese Größe vorbelegt (grow-only, siehe ensureFileSize).
	// 0 = aus (bisheriges Verhalten).
	InitialMmapSize int

	// DataIndexFields deklariert je Event-Typ die `event.data`-Top-Level-Felder,
	// die in den Sekundärindex aufgenommen werden (ADR-029). nil/leer = kein Feld
	// indiziert (Default). Neu deklarierte Felder werden beim Öffnen einmalig über
	// die vorhandenen Events nachindiziert (siehe backfillDataIdx).
	DataIndexFields map[string][]string
}

// Store kapselt die bbolt-Datenbank.
type Store struct {
	// dbMu schützt den db-Pointer gegen den Austausch beim Online-Reopen
	// (Compaction/Grow, ADR-015). Jeder DB-Zugriff hält RLock für die Dauer seiner
	// Transaktion (view/update/batch); der Reopen nimmt Lock exklusiv und wartet
	// damit, bis alle laufenden Transaktionen fertig sind, bevor er den Pointer
	// tauscht. RLock wird bewusst nie verschachtelt (kein reentrant RLock).
	dbMu sync.RWMutex
	db   *bolt.DB

	// initialMmapSize merkt sich die vorab dimensionierte Mmap-/Dateigröße, damit
	// der Reopen (nach Compaction) sie wiederherstellt (sonst kehren die Remaps
	// zurück und die Datei schrumpft auf Datengröße).
	initialMmapSize int

	// pageSize cached die (für die Lebensdauer der Datei konstante) bbolt-
	// Seitengröße. Bewusst NICHT live über db.Info() gelesen: Info() greift ohne
	// mmaplock auf den db.data-Pointer zu und races damit gegen einen gleichzeitigen
	// Write, der einen Mmap-Remap (munmap/mmap) auslöst. Unter dbMu gesetzt
	// (Open/Reopen), lesbar ohne Lock, weil unveränderlich.
	pageSize int

	// lastCompact hält den zuletzt im laufenden Betrieb durchgeführten
	// Online-Compact (CompactInPlace), für die Anzeige im Dashboard/Betrieb.
	// nil = in dieser Laufzeit noch keiner.
	lastCompactMu sync.Mutex
	lastCompact   *CompactionInfo

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

	// compress legt fest, ob neue Event-Werte komprimiert abgelegt werden (ADR-024).
	compress bool

	// dataIdxFields ist die deklarierte (typ → feld-Menge)-Zuordnung des
	// Sekundärindex (ADR-029). Nach dem Öffnen unveränderlich, daher lock-frei
	// lesbar. Leer = Index aus.
	dataIdxFields map[string]map[string]struct{}
}

// Open öffnet (oder erstellt) die Datenbank unter path mit Standardoptionen
// (Group Commit).
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{SyncMode: SyncGroup})
}

// openBolt öffnet die bbolt-Datei mit den für clio relevanten Optionen
// (Timeout, optional InitialMmapSize/SyncOff) und belegt sie — falls
// initialMmapSize > 0 — real auf diese Größe vor. Zentralisiert, weil sowohl der
// erste Open als auch der Online-Reopen (nach Compaction) exakt dieselben
// Optionen brauchen.
func openBolt(path string, initialMmapSize int, syncMode SyncMode) (*bolt.DB, error) {
	boltOpts := &bolt.Options{Timeout: time.Second}
	if initialMmapSize > 0 {
		boltOpts.InitialMmapSize = initialMmapSize
	}
	db, err := bolt.Open(path, 0o600, boltOpts)
	if err != nil {
		return nil, fmt.Errorf("bbolt öffnen: %w", err)
	}
	// SyncOff: bbolt fsync'd nicht mehr beim Commit.
	if syncMode == SyncOff {
		db.NoSync = true
	}
	// Datei real auf die gewünschte Initialgröße bringen. Auf Nicht-Windows lässt
	// bbolt die Datei trotz InitialMmapSize dynamisch wachsen (nur die Mmap ist
	// vorab groß) — wir belegen sie hier einmalig nach dem (korrekt
	// initialisierten) Open vor. Strikt grow-only: eine bereits größere DB bleibt
	// unangetastet. Der Bereich liegt innerhalb der bereits gemappten Region, und
	// es läuft noch keine Transaktion — daher sicher.
	if initialMmapSize > 0 {
		if err := ensureFileSize(db.Path(), int64(initialMmapSize)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("datei vorbelegen: %w", err)
		}
	}
	return db, nil
}

// OpenWithOptions öffnet die Datenbank mit expliziten Optionen und legt die
// nötigen Buckets an.
func OpenWithOptions(path string, opts Options) (*Store, error) {
	db, err := openBolt(path, opts.InitialMmapSize, opts.SyncMode)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketEvents, bucketSubjectIdx, bucketTypeIdx, bucketMeta, bucketTypes, bucketSubjCount, bucketSchemas, bucketDataIdx, bucketAuthKeys, bucketAuditLog} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		// Backfill für Stores, die schon Events enthielten, bevor der
		// types-Bucket eingeführt wurde (ADR-014): ist der Zähler-Bucket leer,
		// aber existieren Events, werden die Typ-Zähler einmalig aus den
		// vorhandenen Events rekonstruiert. Idempotent — bei neuen oder bereits
		// gepflegten Stores ist nichts zu tun.
		if err := backfillTypeCounts(tx); err != nil {
			return err
		}
		// Ebenso den Typ-Index (Typ → Event-Sequenzen) einmalig nachbauen, wenn
		// er leer ist, aber Events existieren (für Stores aus der Zeit vor dem
		// Index). Beschleunigt Typ-Filter in run-query auf Index-Geschwindigkeit.
		if err := backfillTypeIdx(tx); err != nil {
			return err
		}
		// Und die Subject-Zähler (Subject → Anzahl) für die kostenbasierte
		// Index-Wahl in run-query (ADR-023) — ebenfalls idempotenter Backfill.
		if err := backfillSubjCount(tx); err != nil {
			return err
		}
		// Neu deklarierte data-Index-Felder (ADR-029) einmalig über die Historie
		// nachindizieren — idempotent über den covered-Marker im meta-Bucket.
		return backfillDataIdx(tx, normalizeDataIdxFields(opts.DataIndexFields))
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("buckets anlegen: %w", err)
	}

	s := &Store{
		db:              db,
		initialMmapSize: opts.InitialMmapSize,
		pageSize:        db.Info().PageSize, // sicher: noch keine Nebenläufigkeit
		now:             time.Now,
		syncMode:        opts.SyncMode,
		schemaCache:     make(map[string]*jsonschema.Schema),
		dataIdxFields:   normalizeDataIdxFields(opts.DataIndexFields),
	}
	if opts.SigningKey != nil {
		s.signKey = opts.SigningKey
		s.verifyKey = opts.SigningKey.Public().(ed25519.PublicKey)
	}
	s.compress = opts.Compress
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

// view/update/batch führen eine bbolt-Transaktion aus und halten dabei den
// Reopen-Guard (RLock), damit der db-Pointer für die Dauer der Transaktion
// stabil bleibt. Sie dürfen NICHT verschachtelt werden (kein reentrant RLock):
// keine dieser Callbacks ruft eine andere tx-öffnende Store-Methode auf.
func (s *Store) view(fn func(*bolt.Tx) error) error {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.db.View(fn)
}

func (s *Store) update(fn func(*bolt.Tx) error) error {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.db.Update(fn)
}

func (s *Store) batch(fn func(*bolt.Tx) error) error {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.db.Batch(fn)
}

// path liefert den DB-Dateipfad unter dem Reopen-Guard (der Pointer könnte sonst
// während eines Reopen getauscht werden).
func (s *Store) path() string {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.db.Path()
}

// write führt eine Schreibtransaktion gemäß dem konfigurierten SyncMode aus.
// Im Group-Commit-Modus werden gleichzeitige Aufrufe von bbolt coalesced; die
// übergebene Funktion kann dann mehrfach aufgerufen werden und muss daher
// idempotent sein (sie baut ihr Ergebnis bei jedem Lauf frisch auf).
func (s *Store) write(fn func(*bolt.Tx) error) error {
	if s.syncMode == SyncGroup {
		return s.batch(fn)
	}
	return s.update(fn)
}

// reopen schließt die laufende DB, lässt optional mutate die Datei verändern
// (z. B. defragmentieren/ersetzen) und öffnet sie unter denselben Optionen neu —
// alles unter dem exklusiven Reopen-Guard. Er wartet damit, bis alle laufenden
// Transaktionen fertig sind, und blockiert neue, bis der Tausch abgeschlossen
// ist (kurze "Downtime", siehe ADR-015). Schlägt das Wiederöffnen fehl, ist der
// Store anschließend unbrauchbar — der Fehler wird zurückgegeben.
func (s *Store) reopen(mutate func(path string) error) error {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()

	path := s.db.Path()
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("db schließen: %w", err)
	}

	if mutate != nil {
		if mErr := mutate(path); mErr != nil {
			// Mutation fehlgeschlagen: Datei (idealerweise) unverändert — DB wieder
			// öffnen, damit der Store nutzbar bleibt, und beide Fehler melden.
			db, oErr := openBolt(path, s.initialMmapSize, s.syncMode)
			if oErr != nil {
				return errors.Join(mErr, fmt.Errorf("wiederöffnen: %w", oErr))
			}
			s.db = db
			s.pageSize = db.Info().PageSize
			return mErr
		}
	}

	db, err := openBolt(path, s.initialMmapSize, s.syncMode)
	if err != nil {
		return fmt.Errorf("wiederöffnen: %w", err)
	}
	s.db = db
	s.pageSize = db.Info().PageSize // unter Lock, keine Nebenläufigkeit
	return nil
}

// Close schließt die Datenbank.
func (s *Store) Close() error {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	return s.db.Close()
}

// Reset löscht alle Events samt Indizes, Typ-Zählern, Schemas und Ketten-Kopf —
// die Datenbank kehrt in den jungfräulichen Zustand zurück (Tabula rasa) und die
// globale Event-Sequenz beginnt wieder bei 0. Gedacht ist das ausschließlich für
// Entwicklungsumgebungen (Dev-Mode, ADR-022): im Normalbetrieb widerspricht das
// Löschen bewusst der Unveränderlichkeit (vgl. ADR-015, der nur defragmentiert).
// Die Operation ist atomar — alle Buckets werden in einer einzigen Transaktion
// verworfen und frisch angelegt (alles-oder-nichts). Der optionale
// Signaturschlüssel bleibt erhalten (er lebt am Store, nicht in der DB). Liefert
// die Anzahl der gelöschten Events zurück.
func (s *Store) Reset() (uint64, error) {
	var deleted uint64
	err := s.update(func(tx *bolt.Tx) error {
		deleted = tx.Bucket(bucketEvents).Sequence()
		// Bewusst OHNE bucketAuthKeys und bucketAuditLog: der Schlüsselbund
		// (ADR-025) ist mutabler Steuerungs-State (würde der Reset ihn leeren,
		// sperrte man sich aus), und das Audit-Log (ADR-031) muss die Spur des
		// Resets selbst überleben — beide bleiben erhalten.
		for _, name := range [][]byte{bucketEvents, bucketSubjectIdx, bucketTypeIdx, bucketMeta, bucketTypes, bucketSubjCount, bucketSchemas, bucketDataIdx} {
			if tx.Bucket(name) != nil {
				if err := tx.DeleteBucket(name); err != nil {
					return err
				}
			}
			if _, err := tx.CreateBucket(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("datenbank zurücksetzen: %w", err)
	}
	// Kompilierte Schemas verwerfen — die Registrierungen sind weg.
	s.schemaMu.Lock()
	s.schemaCache = make(map[string]*jsonschema.Schema)
	s.schemaMu.Unlock()
	return deleted, nil
}

// Count liefert die Anzahl gespeicherter Events (O(1) über die bbolt-Sequenz).
func (s *Store) Count() (uint64, error) {
	var n uint64
	err := s.view(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketEvents).Sequence()
		return nil
	})
	return n, err
}

// FirstEventTime liefert den Zeitstempel des ersten (ältesten) Events — O(1)
// über das erste Element des nach Sequenz geordneten events-Buckets. ok ist
// false, wenn keine Events existieren. Genutzt, um den Origin des Eventstrom-
// Histogramms zu bestimmen, ohne die gesamte Historie zu scannen (das eigentliche
// Seeding läuft danach asynchron, siehe httpapi.New).
func (s *Store) FirstEventTime() (t time.Time, ok bool, err error) {
	err = s.view(func(tx *bolt.Tx) error {
		k, v := tx.Bucket(bucketEvents).Cursor().First()
		if k == nil {
			return nil
		}
		var rec struct {
			Time string `json:"time"`
		}
		if err := unmarshalStored(v, &rec); err != nil {
			return nil // ungültig → wie „keine Events", Origin fällt auf Default
		}
		parsed, perr := time.Parse(time.RFC3339Nano, rec.Time)
		if perr != nil {
			return nil
		}
		t, ok = parsed.UTC(), true
		return nil
	})
	return t, ok, err
}

// CountByTypes liefert die Gesamtzahl der Events über die angegebenen Typen
// (Summe der Typ-Zähler, O(len(types))). Grundlage der kostenbasierten
// Index-Wahl in run-query (ADR-023): die Kosten eines Typ-Index-Scans.
func (s *Store) CountByTypes(types []string) (uint64, error) {
	var total uint64
	err := s.view(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTypes)
		for _, t := range types {
			if v := b.Get([]byte(t)); len(v) == 8 {
				total += binary.BigEndian.Uint64(v)
			}
		}
		return nil
	})
	return total, err
}

// CountSubject liefert die Anzahl der Events im Subject-Scope (exakt) — die
// Kosten eines Subject-Index-Scans für die Index-Wahl (ADR-023). Über den
// subj_count-Bucket ist das günstig: Wurzel = O(1) (Gesamtzahl), nicht-rekursiv
// = ein Lookup, rekursiv = O(distinkte Subjects im Teilbaum) statt O(Events).
// Prefix-Geschwister (z. B. /booksstore zu /books) werden via MatchSubject
// ausgeschlossen.
func (s *Store) CountSubject(subject string, recursive bool) (uint64, error) {
	if subject == "/" {
		return s.Count() // Wurzel = alle Events
	}
	var total uint64
	err := s.view(func(tx *bolt.Tx) error {
		subjc := tx.Bucket(bucketSubjCount)
		if !recursive {
			if v := subjc.Get([]byte(subject)); len(v) == 8 {
				total = binary.BigEndian.Uint64(v)
			}
			return nil
		}
		prefix := []byte(subject)
		c := subjc.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			if !MatchSubject(string(k), subject, true) {
				continue
			}
			if len(v) == 8 {
				total += binary.BigEndian.Uint64(v)
			}
		}
		return nil
	})
	return total, err
}

// ForEachEventTimeSource ruft fn für jedes gespeicherte Event mit dessen
// Zeitstempel und CloudEvents-`source` auf, in globaler Reihenfolge (=
// Schreibreihenfolge). Es werden nur die Felder `time` und `source` dekodiert
// (günstig), damit Aufrufer z. B. ein Zeit-Histogramm der gesamten Historie —
// optional nach Source aufgeschlüsselt — aufbauen können, ohne die Events
// vollständig zu laden. Events mit fehlendem/ungültigem Zeitstempel werden
// übersprungen.
//
// Ist maxSeq > 0, werden nur Events bis einschließlich dieser globalen Sequenz
// berücksichtigt (der events-Bucket ist nach Sequenz geordnet, der Scan bricht
// danach ab). So kann ein asynchroner Seeder eine saubere Grenze gegen
// gleichzeitig neu geschriebene Events ziehen (maxSeq = 0 → keine Grenze).
func (s *Store) ForEachEventTimeSource(maxSeq uint64, fn func(time.Time, string)) error {
	return s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketEvents).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if maxSeq != 0 && binary.BigEndian.Uint64(k) > maxSeq {
				break
			}
			var rec struct {
				Time   string `json:"time"`
				Source string `json:"source"`
			}
			if err := unmarshalStored(v, &rec); err != nil {
				continue
			}
			t, err := time.Parse(time.RFC3339Nano, rec.Time)
			if err != nil {
				continue
			}
			fn(t.UTC(), rec.Source)
		}
		return nil
	})
}

// TypeInfo beschreibt einen bisher geschriebenen Event-Typ.
type TypeInfo struct {
	Type      string `json:"type"`
	Count     uint64 `json:"count"`
	HasSchema bool   `json:"hasSchema"`
}

// SubjectInfo beschreibt ein bisher beschriebenes Subject (einen Stream) samt
// der Anzahl seiner Events.
type SubjectInfo struct {
	Subject string `json:"subject"`
	Count   uint64 `json:"count"`
}

// Subjects liefert alle bisher beschriebenen Subjects (Streams) in
// alphabetischer Reihenfolge (bbolt-Schlüsselordnung) samt Event-Anzahl. Die
// Zählung läuft als einzelner Scan über den Subject-Index: dessen Schlüssel
// (`subject` + Trenner + seq) liegen pro Subject zusammenhängend, daher genügt
// ein Durchlauf ohne die Events selbst zu laden (~O(Index)).
//
// Ist prefix nicht leer, werden nur Subjects im rekursiven Scope von prefix
// zurückgegeben (prefix selbst und alles darunter). Prefix-Geschwister wie
// `/booksstore` zu `/books` werden via MatchSubject ausgeschlossen.
func (s *Store) Subjects(prefix string) ([]SubjectInfo, error) {
	var out []SubjectInfo
	err := s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketSubjectIdx).Cursor()
		var seek []byte
		if prefix != "" {
			seek = []byte(prefix)
		}
		var (
			started bool
			curSubj string
			cnt     uint64
		)
		flush := func() {
			if started && (prefix == "" || MatchSubject(curSubj, prefix, true)) {
				out = append(out, SubjectInfo{Subject: curSubj, Count: cnt})
			}
		}
		// Seek(nil) verhält sich wie First(). Bei gesetztem prefix bricht der
		// Scan ab, sobald der Byte-Prefix nicht mehr passt (sortierte Keys).
		for k, _ := c.Seek(seek); k != nil; k, _ = c.Next() {
			if seek != nil && !hasPrefix(k, seek) {
				break
			}
			sep := bytes.IndexByte(k, subjectSep)
			if sep < 0 {
				continue
			}
			subj := string(k[:sep])
			if !started || subj != curSubj {
				flush()
				curSubj = subj
				cnt = 0
				started = true
			}
			cnt++
		}
		flush()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ChildInfo beschreibt ein direktes Kind-Segment unterhalb eines Eltern-Pfads
// im Subject-Baum. Count sind die Events exakt auf diesem Kind-Subject (0 für
// reine Zwischenknoten), Total die aggregierte Anzahl im gesamten Teilbaum des
// Kindes, HasChildren zeigt an, ob das Kind selbst weitere Nachfahren hat.
type ChildInfo struct {
	Subject     string `json:"subject"`
	Count       uint64 `json:"count"`
	Total       uint64 `json:"total"`
	HasChildren bool   `json:"hasChildren"`
}

// Children liefert die direkten Kinder unterhalb von parent ("/" oder "/a/b")
// seitenweise und alphabetisch sortiert. after (exklusiv) ist das zuletzt
// gelesene Kind-Subject einer vorherigen Seite ("" = von vorne). limit > 0
// begrenzt die Anzahl Kinder; der zweite Rückgabewert meldet, ob darüber hinaus
// weitere Kinder folgen (Fortsetzungs-Cursor = Subject des letzten Kindes).
//
// Anders als Subjects arbeitet Children auf dem subj_count-Bucket (ein Eintrag
// je distinktem Subject) statt auf dem per-Event-Index und bricht nach `limit`
// fertigen Kindern ab. Damit skaliert das Aufklappen eines Knotens mit der Zahl
// der distinkten Subjects — nicht mit der Gesamtzahl der Events.
func (s *Store) Children(parent, after string, limit int) ([]ChildInfo, bool, error) {
	// Scan-Präfix: unter der Wurzel beginnen alle Subjects mit "/", sonst mit
	// "parent/". Der Kind-Pfad ist scanPrefix + erstes Segment bis zum nächsten "/".
	scanPrefix := "/"
	if parent != "/" && parent != "" {
		scanPrefix = parent + "/"
	}
	sp := []byte(scanPrefix)
	afterSub := []byte(after)
	afterChild := []byte(after + "/")

	var (
		out  []ChildInfo
		more bool
	)
	err := s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketSubjCount).Cursor()
		seek := sp
		if after != "" {
			seek = afterSub
		}

		var cur *ChildInfo
		// finalize hängt das aktuelle (vollständige) Kind an out an. Ist das Limit
		// erreicht, ist dieses Kind das erste „darüber hinaus" → more=true, stop.
		finalize := func() bool {
			if cur == nil {
				return true
			}
			if limit > 0 && len(out) >= limit {
				more = true
				return false
			}
			out = append(out, *cur)
			cur = nil
			return true
		}

		for k, v := c.Seek(seek); k != nil && hasPrefix(k, sp); k, v = c.Next() {
			// after und dessen gesamten Teilbaum überspringen.
			if after != "" && (bytes.Equal(k, afterSub) || hasPrefix(k, afterChild)) {
				continue
			}
			rest := k[len(sp):]
			if len(rest) == 0 {
				continue // das Eltern-Subject selbst ist kein Kind
			}
			slash := bytes.IndexByte(rest, '/')
			deeper := slash >= 0
			childPath := string(k)
			if deeper {
				childPath = string(k[:len(sp)+slash])
			}
			if cur != nil && cur.Subject != childPath {
				if !finalize() {
					return nil
				}
			}
			if cur == nil {
				cur = &ChildInfo{Subject: childPath}
			}
			var cnt uint64
			if len(v) == 8 {
				cnt = binary.BigEndian.Uint64(v)
			}
			cur.Total += cnt
			if deeper {
				cur.HasChildren = true
			} else {
				cur.Count = cnt
			}
		}
		finalize()
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, more, nil
}

// DirectCount liefert die Anzahl Events exakt auf subject (ohne Nachfahren).
// Im Gegensatz zu CountSubject(subject, false) wird auch die Wurzel "/" als
// exaktes Subject behandelt (Events direkt auf "/"), nicht als Gesamtsumme.
func (s *Store) DirectCount(subject string) (uint64, error) {
	var n uint64
	err := s.view(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketSubjCount).Get([]byte(subject)); len(v) == 8 {
			n = binary.BigEndian.Uint64(v)
		}
		return nil
	})
	return n, err
}

// EventTypes liefert alle bisher geschriebenen Event-Typen in alphabetischer
// Reihenfolge (bbolt-Schlüsselordnung) samt Anzahl und ob ein Schema registriert
// ist.
func (s *Store) EventTypes() ([]TypeInfo, error) {
	var out []TypeInfo
	err := s.view(func(tx *bolt.Tx) error {
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

// backfillTypeCounts rekonstruiert die Typ-Zähler aus den vorhandenen Events,
// falls der types-Bucket leer ist, aber bereits Events existieren. Das tritt bei
// Stores auf, die vor der Einführung des types-Buckets (ADR-014) befüllt wurden:
// dort liefert read-event-types sonst nichts, obwohl Events vorhanden sind. Ist
// der types-Bucket bereits befüllt oder gibt es keine Events, passiert nichts.
func backfillTypeCounts(tx *bolt.Tx) error {
	types := tx.Bucket(bucketTypes)
	if k, _ := types.Cursor().First(); k != nil {
		return nil // bereits gepflegt
	}
	evts := tx.Bucket(bucketEvents)
	c := evts.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var ev event.Event
		if err := unmarshalStored(v, &ev); err != nil {
			return fmt.Errorf("event für types-backfill dekodieren: %w", err)
		}
		if err := incrTypeCount(types, ev.Type); err != nil {
			return err
		}
	}
	return nil
}

// backfillTypeIdx baut den Typ-Index (Typ → Event-Sequenzen) einmalig aus den
// vorhandenen Events nach, falls er leer ist, aber bereits Events existieren.
// Idempotent — bei neuen oder bereits indizierten Stores passiert nichts.
func backfillTypeIdx(tx *bolt.Tx) error {
	tidx := tx.Bucket(bucketTypeIdx)
	if k, _ := tidx.Cursor().First(); k != nil {
		return nil // bereits indiziert
	}
	evts := tx.Bucket(bucketEvents)
	c := evts.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var ev event.Event
		if err := unmarshalStored(v, &ev); err != nil {
			return fmt.Errorf("event für type-index-backfill dekodieren: %w", err)
		}
		seq := binary.BigEndian.Uint64(k)
		if err := tidx.Put(typeKey(ev.Type, seq), k); err != nil {
			return err
		}
	}
	return nil
}

// ReadByTypesFunc streamt — über den Typ-Index — alle Events der angegebenen
// Typen in globaler Reihenfolge (Sequenz), gefiltert nach lowerBound/upperBound,
// und ruft fn je Event auf. Liefert fn false, bricht der Scan ab (für Limit).
// Subject-Scope und weitere Prädikate prüft der Aufrufer. So werden bei
// Typ-Filtern nur die Treffer geladen statt der gesamte Store gescannt.
func (s *Store) ReadByTypesFunc(types []string, opts ReadOptions, fn func(event.Event) bool) error {
	return s.view(func(tx *bolt.Tx) error {
		tidx := tx.Bucket(bucketTypeIdx)
		evts := tx.Bucket(bucketEvents)
		if tidx == nil {
			return nil
		}

		load := func(seq uint64) (event.Event, bool, error) {
			if !inBounds(seq, opts) {
				return event.Event{}, false, nil
			}
			raw := evts.Get(seqKey(seq))
			if raw == nil {
				return event.Event{}, false, fmt.Errorf("inkonsistenter type-index: event %d fehlt", seq)
			}
			var ev event.Event
			if err := unmarshalStored(raw, &ev); err != nil {
				return event.Event{}, false, fmt.Errorf("event dekodieren: %w", err)
			}
			return ev, true, nil
		}

		// Ein Typ: direkt vom Cursor in Sequenzreihenfolge streamen (früher Abbruch
		// möglich). Mehrere Typen: Sequenzen sammeln, global sortieren, dann laden.
		if len(types) == 1 {
			prefix := typePrefix(types[0])
			cur := tidx.Cursor()
			for k, v := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = cur.Next() {
				if len(k) != len(prefix)+8 {
					continue // exakter Typ (keine Separator-Kollision)
				}
				ev, ok, err := load(binary.BigEndian.Uint64(v))
				if err != nil {
					return err
				}
				if ok && !fn(ev) {
					return nil
				}
			}
			return nil
		}

		var seqs []uint64
		for _, t := range types {
			prefix := typePrefix(t)
			cur := tidx.Cursor()
			for k, v := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = cur.Next() {
				if len(k) != len(prefix)+8 {
					continue
				}
				seqs = append(seqs, binary.BigEndian.Uint64(v))
			}
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for _, seq := range seqs {
			ev, ok, err := load(seq)
			if err != nil {
				return err
			}
			if ok && !fn(ev) {
				return nil
			}
		}
		return nil
	})
}

// normalizeDataIdxFields wandelt die deklarierte (typ → feld-Liste)-Konfig in
// eine (typ → feld-Menge)-Form für O(1)-Mitgliedschaftsprüfungen. Leere/triviale
// Einträge werden verworfen; ohne Felder ergibt sich nil (Index aus).
func normalizeDataIdxFields(in map[string][]string) map[string]map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]struct{}, len(in))
	for typ, fields := range in {
		if typ == "" {
			continue
		}
		set := make(map[string]struct{}, len(fields))
		for _, f := range fields {
			if f != "" {
				set[f] = struct{}{}
			}
		}
		if len(set) > 0 {
			out[typ] = set
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DataFieldIndexed meldet, ob für (typ,feld) ein gepflegter Daten-Index existiert
// (ADR-029) — die Voraussetzung dafür, dass run-query ein
// `event.data.<feld> == '<wert>'`-Prädikat per Index statt per Voll-Scan
// beantworten darf.
func (s *Store) DataFieldIndexed(typ, field string) bool {
	fields, ok := s.dataIdxFields[typ]
	if !ok {
		return false
	}
	_, ok = fields[field]
	return ok
}

// dataIdxPrefix bildet den Seek-Prefix typ\x00feld\x00len(wert)\x00wert für eine
// Gleichheits-Abfrage. Der Wert wird längenpräfigiert, damit ein Wert nie der
// Präfix eines anderen sein kann (z. B. "a" vs. "ab") — sonst lieferte ein
// Prefix-Seek falsche Treffer. typ/feld enthalten keinen Separator (0x00).
func dataIdxPrefix(typ, field string, value []byte) []byte {
	p := make([]byte, 0, len(typ)+1+len(field)+1+4+len(value))
	p = append(p, typ...)
	p = append(p, subjectSep)
	p = append(p, field...)
	p = append(p, subjectSep)
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(value)))
	p = append(p, lp[:]...)
	return append(p, value...)
}

// dataIdxKey bildet den vollständigen Daten-Index-Schlüssel (Prefix + seq).
func dataIdxKey(typ, field string, value []byte, seq uint64) []byte {
	return append(dataIdxPrefix(typ, field, value), seqKey(seq)...)
}

// indexDataFields schreibt für ein Event die Index-Einträge aller für seinen Typ
// deklarierten Felder. Nur Top-Level-`data`-Felder mit String-Wert werden
// indiziert (v1, ADR-029); fehlende oder nicht-stringwertige Felder werden
// übersprungen. data ist die kanonisch kompaktierte Payload (wie gespeichert).
func indexDataFields(didx *bolt.Bucket, fields map[string]struct{}, typ string, data json.RawMessage, seq uint64) error {
	if len(fields) == 0 || len(data) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil // kein JSON-Objekt → nichts zu indizieren (kein Fehler)
	}
	key := seqKey(seq)
	for field := range fields {
		raw, ok := obj[field]
		if !ok {
			continue
		}
		s, ok := decodeJSONString(raw)
		if !ok {
			continue // v1: nur String-Werte
		}
		if err := didx.Put(dataIdxKey(typ, field, []byte(s), seq), key); err != nil {
			return err
		}
	}
	return nil
}

// decodeJSONString liefert den String-Wert eines JSON-RawMessage, falls es ein
// JSON-String ist (führendes '"'). So bleibt das Indexieren auf String-Felder
// beschränkt, ohne Zahlen/Objekte teuer zu dekodieren.
func decodeJSONString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// ReadByDataFieldFunc streamt — über den Daten-Index (ADR-029) — alle Events des
// Typs typ, deren Top-Level-`data`-Feld field exakt dem String value entspricht,
// in globaler Sequenzreihenfolge und ruft fn je Event auf. Liefert fn false,
// bricht der Scan ab (für Limit). lowerBound/upperBound werden respektiert.
// Subject-Scope und das vollständige Prädikat prüft der Aufrufer — dieser Index
// liefert eine notwendige Bedingung (Gleichheit), nie ein zu enges Ergebnis.
func (s *Store) ReadByDataFieldFunc(typ, field, value string, opts ReadOptions, fn func(event.Event) bool) error {
	return s.view(func(tx *bolt.Tx) error {
		didx := tx.Bucket(bucketDataIdx)
		evts := tx.Bucket(bucketEvents)
		if didx == nil {
			return nil
		}
		prefix := dataIdxPrefix(typ, field, []byte(value))
		cur := didx.Cursor()
		for k, v := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = cur.Next() {
			if len(k) != len(prefix)+8 {
				continue // exakter Wert (keine Längen-/Separator-Kollision)
			}
			seq := binary.BigEndian.Uint64(v)
			if !inBounds(seq, opts) {
				continue
			}
			raw := evts.Get(seqKey(seq))
			if raw == nil {
				return fmt.Errorf("inkonsistenter data-index: event %d fehlt", seq)
			}
			var ev event.Event
			if err := unmarshalStored(raw, &ev); err != nil {
				return fmt.Errorf("event dekodieren: %w", err)
			}
			if !fn(ev) {
				return nil
			}
		}
		return nil
	})
}

// backfillDataIdx indiziert beim Öffnen die deklarierten (typ,feld)-Paare, die
// laut covered-Marker im meta-Bucket noch nicht über die gesamte Historie
// aufgebaut wurden — in einem einzigen Scan über alle Events. Idempotent: bereits
// abgedeckte Felder werden übersprungen, ein leerer Store/keine offenen Felder
// kostet nichts. Felder, die aus der Konfig entfernt wurden, bleiben als covered
// markiert (ihre Alt-Einträge stören nicht; der Planner fragt nur deklarierte
// Felder ab).
func backfillDataIdx(tx *bolt.Tx, fields map[string]map[string]struct{}) error {
	if len(fields) == 0 {
		return nil
	}
	meta := tx.Bucket(bucketMeta)
	covered := loadCoveredDataIdx(meta)

	// Offene (noch nicht abgedeckte) Felder je Typ bestimmen.
	pending := make(map[string]map[string]struct{})
	for typ, set := range fields {
		for field := range set {
			if _, done := covered[typ+"\x00"+field]; done {
				continue
			}
			if pending[typ] == nil {
				pending[typ] = make(map[string]struct{})
			}
			pending[typ][field] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return nil
	}

	didx := tx.Bucket(bucketDataIdx)
	evts := tx.Bucket(bucketEvents)
	c := evts.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var ev event.Event
		if err := unmarshalStored(v, &ev); err != nil {
			return fmt.Errorf("event für data-index-backfill dekodieren: %w", err)
		}
		set := pending[ev.Type]
		if len(set) == 0 {
			continue
		}
		if err := indexDataFields(didx, set, ev.Type, ev.Data, binary.BigEndian.Uint64(k)); err != nil {
			return err
		}
	}

	for typ, set := range pending {
		for field := range set {
			covered[typ+"\x00"+field] = struct{}{}
		}
	}
	return storeCoveredDataIdx(meta, covered)
}

// loadCoveredDataIdx liest die Menge bereits indizierter (typ,feld)-Paare aus dem
// meta-Bucket (JSON-Array aus "typ\x00feld"-Strings).
func loadCoveredDataIdx(meta *bolt.Bucket) map[string]struct{} {
	out := make(map[string]struct{})
	raw := meta.Get(metaDataIdxCovered)
	if len(raw) == 0 {
		return out
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return out // korrupter Marker → als „nichts abgedeckt" behandeln (neu aufbauen)
	}
	for _, s := range list {
		out[s] = struct{}{}
	}
	return out
}

// storeCoveredDataIdx schreibt die covered-Menge zurück in den meta-Bucket.
func storeCoveredDataIdx(meta *bolt.Bucket, covered map[string]struct{}) error {
	list := make([]string, 0, len(covered))
	for s := range covered {
		list = append(list, s)
	}
	sort.Strings(list)
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return meta.Put(metaDataIdxCovered, raw)
}

// typePrefix bildet den Index-Prefix type + sep (zum Seek je Typ).
func typePrefix(typ string) []byte {
	p := make([]byte, 0, len(typ)+1)
	p = append(p, typ...)
	return append(p, subjectSep)
}

// typeKey bildet den Typ-Index-Schlüssel type + sep + seq.
func typeKey(typ string, seq uint64) []byte {
	return append(typePrefix(typ), seqKey(seq)...)
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

// incrSubjectCount erhöht den Event-Zähler eines Subjects im subj_count-Bucket
// (für die kostenbasierte Index-Wahl in run-query, ADR-023).
func incrSubjectCount(subjc *bolt.Bucket, subject string) error {
	var cnt uint64
	if v := subjc.Get([]byte(subject)); len(v) == 8 {
		cnt = binary.BigEndian.Uint64(v)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], cnt+1)
	return subjc.Put([]byte(subject), buf[:])
}

// backfillSubjCount rekonstruiert die Subject-Zähler aus den vorhandenen Events,
// falls der subj_count-Bucket leer ist, aber bereits Events existieren (Stores
// aus der Zeit vor dem Zähler). Idempotent — sonst passiert nichts.
func backfillSubjCount(tx *bolt.Tx) error {
	subjc := tx.Bucket(bucketSubjCount)
	if k, _ := subjc.Cursor().First(); k != nil {
		return nil // bereits gepflegt
	}
	evts := tx.Bucket(bucketEvents)
	c := evts.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var ev event.Event
		if err := unmarshalStored(v, &ev); err != nil {
			return fmt.Errorf("event für subj-count-backfill dekodieren: %w", err)
		}
		if err := incrSubjectCount(subjc, ev.Subject); err != nil {
			return err
		}
	}
	return nil
}

// Append prüft die Preconditions und speichert anschließend eine oder mehrere
// Candidates atomar (alles-oder-nichts). Schlägt eine Precondition fehl, wird
// nichts geschrieben und ein in ErrPreconditionFailed gehüllter Fehler
// zurückgegeben. Bei leerer Eingabe wird ein leeres Ergebnis zurückgegeben.
func (s *Store) Append(candidates []event.Candidate, preconditions []Precondition) ([]event.Event, error) {
	return s.AppendAuthored(candidates, preconditions, "")
}

// AppendAuthored schreibt wie Append, stempelt aber zusätzlich die Urheberschaft
// (kid des authentifizierten Schlüssels, ADR-025) auf jedes Event. authKID == ""
// verhält sich byte-identisch zu Append (keine Urheberschaft, kein Hash-Einfluss).
// Der Wert stammt serverseitig aus der authentifizierten Identität — ein Client
// kann ihn nicht selbst setzen.
func (s *Store) AppendAuthored(candidates []event.Candidate, preconditions []Precondition, authKID string) ([]event.Event, error) {
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
		tidx := tx.Bucket(bucketTypeIdx)
		meta := tx.Bucket(bucketMeta)
		types := tx.Bucket(bucketTypes)
		subjc := tx.Bucket(bucketSubjCount)
		didx := tx.Bucket(bucketDataIdx)

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
				AuthKID:         authKID,
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
			stored, err := encodeStored(payload, s.compress)
			if err != nil {
				return err
			}

			key := seqKey(seq)
			if err := evts.Put(key, stored); err != nil {
				return err
			}
			if err := idx.Put(subjectKey(c.Subject, seq), key); err != nil {
				return err
			}
			if err := tidx.Put(typeKey(c.Type, seq), key); err != nil {
				return err
			}
			if err := incrTypeCount(types, c.Type); err != nil {
				return err
			}
			if err := incrSubjectCount(subjc, c.Subject); err != nil {
				return err
			}
			// Sekundärindex auf deklarierte data-Felder pflegen (ADR-029). data ist
			// hier die kanonisch kompaktierte Form (wie gespeichert) — identisch zur
			// Backfill-Quelle, damit Schreib- und Backfill-Pfad denselben Wert ablegen.
			if err := indexDataFields(didx, s.dataIdxFields[c.Type], c.Type, data, seq); err != nil {
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
	var res VerifyResult
	err := s.view(func(tx *bolt.Tx) error {
		r, e := verifyChain(tx, s.verifyKey)
		res = r
		return e
	})
	if err != nil {
		return VerifyResult{}, err
	}
	return res, nil
}

// verifyChain rechnet die Hash-Kette über die Events einer (Lese-)Transaktion
// nach. Gemeinsame Grundlage für die Online-Prüfung (Store.Verify) und die
// Offline-Prüfung einer beliebigen DB-/Backup-Datei (VerifyFile, ADR-026). Ist
// verifyKey gesetzt, werden vorhandene Signaturen mitgeprüft. Ein
// Dekodier-Fehler eines Events wird als Fehler zurückgegeben (interner Fehler),
// ein inhaltlicher Bruch der Kette als VerifyResult{OK:false}.
func verifyChain(tx *bolt.Tx, verifyKey ed25519.PublicKey) (VerifyResult, error) {
	res := VerifyResult{OK: true, Head: event.GenesisHash}
	prev := event.GenesisHash

	eb := tx.Bucket(bucketEvents)
	if eb == nil {
		return res, nil
	}
	c := eb.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var ev event.Event
		if err := unmarshalStored(v, &ev); err != nil {
			return VerifyResult{}, fmt.Errorf("event dekodieren: %w", err)
		}
		res.Count++

		if ev.PredecessorHash != prev {
			res.OK = false
			res.BrokenAt = ev.ID
			res.Reason = "predecessorhash passt nicht zum Vorgänger"
			return res, nil
		}
		if want := event.ComputeHash(ev); ev.Hash != want {
			res.OK = false
			res.BrokenAt = ev.ID
			res.Reason = "hash stimmt nicht mit dem Inhalt überein"
			return res, nil
		}
		// Signatur prüfen, sofern vorhanden und ein Schlüssel konfiguriert ist.
		if verifyKey != nil && ev.Signature != nil {
			if err := verifySignature(verifyKey, ev.Hash, *ev.Signature); err != nil {
				res.OK = false
				res.BrokenAt = ev.ID
				res.Reason = "signatur ungültig: " + err.Error()
				return res, nil
			}
		}
		prev = ev.Hash
	}

	res.Head = prev
	// Gespeicherter Ketten-Kopf muss zum letzten Event passen.
	storedHead := event.GenesisHash
	if mb := tx.Bucket(bucketMeta); mb != nil {
		if h := mb.Get(metaChainHead); len(h) > 0 {
			storedHead = string(h)
		}
	}
	if storedHead != prev {
		res.OK = false
		res.Reason = "gespeicherter Ketten-Kopf passt nicht zum letzten Event"
	}
	return res, nil
}

// checkPreconditions wertet alle Preconditions innerhalb der Schreibtransaktion
// aus, damit Prüfung und Write atomar sind.
func checkPreconditions(tx *bolt.Tx, preconditions []Precondition) error {
	idx := tx.Bucket(bucketSubjectIdx)

	for _, p := range preconditions {
		switch p.Type {
		case PreconditionSubjectPristine:
			if _, exists := lastSeq(idx, p.Subject); exists {
				return fmt.Errorf("%w: subject %q ist nicht leer", ErrPreconditionFailed, p.Subject)
			}
		case PreconditionSubjectOnEventID:
			want, err := strconv.ParseUint(p.EventID, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: ungültige eventId %q", ErrPreconditionFailed, p.EventID)
			}
			last, exists := lastSeq(idx, p.Subject)
			if !exists {
				return fmt.Errorf("%w: subject %q ist leer, erwartet eventId %s", ErrPreconditionFailed, p.Subject, p.EventID)
			}
			if last != want {
				return fmt.Errorf("%w: subject %q steht auf eventId %d, erwartet %d", ErrPreconditionFailed, p.Subject, last, want)
			}
		case PreconditionQueryResultEmpty, PreconditionQueryResultNonEmpty:
			matched, err := anyMatch(tx, p.Subject, p.Recursive, p.Predicate)
			if err != nil {
				return err
			}
			if p.Type == PreconditionQueryResultEmpty && matched {
				return fmt.Errorf("%w: abfrage über %q liefert treffer, erwartet leeres ergebnis", ErrPreconditionFailed, p.Subject)
			}
			if p.Type == PreconditionQueryResultNonEmpty && !matched {
				return fmt.Errorf("%w: abfrage über %q liefert kein ergebnis, erwartet mindestens einen treffer", ErrPreconditionFailed, p.Subject)
			}
		default:
			return fmt.Errorf("%w: unbekannter precondition-typ %q", ErrPreconditionFailed, p.Type)
		}
	}
	return nil
}

// anyMatch prüft, ob im Scope (subject, recursive) mindestens ein Event zum
// Prädikat passt (pred nil = jedes Event zählt). Auswertungsfehler eines Events
// gelten als „kein Treffer". Läuft innerhalb der Schreibtransaktion.
func anyMatch(tx *bolt.Tx, subject string, recursive bool, pred *query.Predicate) (bool, error) {
	check := func(ev event.Event) bool {
		if pred == nil {
			return true
		}
		ok, err := pred.Eval(ev)
		return err == nil && ok
	}

	if recursive {
		// Wurzel: gesamtes events-Bucket. Sonst über den Subject-Index begrenzen
		// (Early-Exit beim ersten Treffer).
		if subject == "/" {
			cur := tx.Bucket(bucketEvents).Cursor()
			for k, v := cur.First(); k != nil; k, v = cur.Next() {
				var ev event.Event
				if err := unmarshalStored(v, &ev); err != nil {
					return false, fmt.Errorf("event dekodieren: %w", err)
				}
				if check(ev) {
					return true, nil
				}
			}
			return false, nil
		}

		evts := tx.Bucket(bucketEvents)
		cur := tx.Bucket(bucketSubjectIdx).Cursor()
		prefix := []byte(subject)
		for k, evKey := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, evKey = cur.Next() {
			sep := bytes.IndexByte(k, subjectSep)
			if sep < 0 || !MatchSubject(string(k[:sep]), subject, true) {
				continue
			}
			raw := evts.Get(evKey)
			if raw == nil {
				return false, fmt.Errorf("inkonsistenter index: event %x fehlt", evKey)
			}
			var ev event.Event
			if err := unmarshalStored(raw, &ev); err != nil {
				return false, fmt.Errorf("event dekodieren: %w", err)
			}
			if check(ev) {
				return true, nil
			}
		}
		return false, nil
	}

	evts := tx.Bucket(bucketEvents)
	cur := tx.Bucket(bucketSubjectIdx).Cursor()
	prefix := append([]byte(subject), subjectSep)
	for k, evKey := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, evKey = cur.Next() {
		raw := evts.Get(evKey)
		if raw == nil {
			return false, fmt.Errorf("inkonsistenter index: event %x fehlt", evKey)
		}
		var ev event.Event
		if err := unmarshalStored(raw, &ev); err != nil {
			return false, fmt.Errorf("event dekodieren: %w", err)
		}
		if check(ev) {
			return true, nil
		}
	}
	return false, nil
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
	err := s.ReadFunc(query, recursive, opts, func(ev event.Event) bool {
		events = append(events, ev)
		return true
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// ReadFunc ist die streamende Variante von Read: statt alle Treffer in ein Slice
// zu materialisieren (O(Treffer) Speicher), ruft es fn für jedes passende Event
// in globaler Schreibreihenfolge auf. Gibt fn false zurück, bricht der Scan
// vorzeitig (und speicherschonend) ab — so kann der Aufrufer ein Limit
// durchsetzen, ohne erst den gesamten Scope zu laden. Der Speicherbedarf bleibt
// damit konstant, unabhängig von der Größe der Historie.
func (s *Store) ReadFunc(query string, recursive bool, opts ReadOptions, fn func(event.Event) bool) error {
	return s.view(func(tx *bolt.Tx) error {
		if recursive {
			return readRecursive(tx, query, opts, fn)
		}
		return readSubjectIndex(tx, query, opts, fn)
	})
}

func readSubjectIndex(tx *bolt.Tx, subject string, opts ReadOptions, fn func(event.Event) bool) error {
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
		if err := unmarshalStored(raw, &ev); err != nil {
			return fmt.Errorf("event dekodieren: %w", err)
		}
		if !matchType(ev.Type, types) {
			continue
		}
		if !fn(ev) {
			return nil
		}
	}
	return nil
}

// maxRecursiveSeqBuffer deckelt, wie viele Treffer-Sequenzen ein rekursiver
// Nicht-Wurzel-Read im Speicher sammelt, bevor er auf den streamenden globalen
// Scan zurückfällt. So bleibt der Speicher eines einzelnen breiten Reads
// beschränkt (≈ 8 Byte je Eintrag → hier ~512 KB), statt bei einem Teilbaum mit
// Millionen Events eine entsprechend riesige Seq-Liste zu allokieren. Variable
// (nicht const), damit Tests den Schwellwert herabsetzen können.
var maxRecursiveSeqBuffer = 1 << 16

func readRecursive(tx *bolt.Tx, query string, opts ReadOptions, fn func(event.Event) bool) error {
	// Wurzel: das Subtree ist „alles" — der nach Sequenz geordnete events-Bucket
	// ist hier optimal (eine Index-Umleitung wäre nur teurer).
	if query == "/" {
		return scanEventsRecursive(tx, query, opts, fn)
	}

	// Nicht-Wurzel: über den Subject-Index begrenzen, statt den gesamten
	// events-Bucket zu scannen. Index-Schlüssel sind subject + sep + seq; wir
	// betrachten nur Schlüssel mit dem literalen Prefix `query`, sammeln die
	// passenden Sequenzen, sortieren sie (globale Ordnung) und laden gezielt.
	//
	// Speicherschutz (Skalierung auf Millionen Events): Übersteigt die Treffermenge
	// maxRecursiveSeqBuffer, wird die Teil-Sammlung verworfen und stattdessen der
	// streamende globale Scan genutzt (O(1) Speicher, identische globale Ordnung,
	// per fn früh abbrechbar). Bis zu diesem Schwellwert bleibt der schnelle,
	// index-begrenzte Pfad für kleine/mittlere Teilbäume erhalten.
	idx := tx.Bucket(bucketSubjectIdx)
	evts := tx.Bucket(bucketEvents)
	types := opts.typeSet()
	prefix := []byte(query)

	seqs := make([]uint64, 0, 1024)
	cur := idx.Cursor()
	for k, evKey := cur.Seek(prefix); k != nil && hasPrefix(k, prefix); k, evKey = cur.Next() {
		sep := bytes.IndexByte(k, subjectSep)
		if sep < 0 || !MatchSubject(string(k[:sep]), query, true) {
			continue
		}
		seq := binary.BigEndian.Uint64(evKey)
		if !inBounds(seq, opts) {
			continue
		}
		if len(seqs) >= maxRecursiveSeqBuffer {
			// Großer Teilbaum: speicherschonend global scannen statt alle Seqs zu
			// halten. Es wurde noch nichts emittiert → kein Doppel-/Teil-Output.
			return scanEventsRecursive(tx, query, opts, fn)
		}
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	for _, seq := range seqs {
		raw := evts.Get(seqKey(seq))
		if raw == nil {
			return fmt.Errorf("inkonsistenter index: event %x fehlt", seqKey(seq))
		}
		var ev event.Event
		if err := unmarshalStored(raw, &ev); err != nil {
			return fmt.Errorf("event dekodieren: %w", err)
		}
		if matchType(ev.Type, types) {
			if !fn(ev) {
				return nil
			}
		}
	}
	return nil
}

// scanEventsRecursive durchläuft den global nach Sequenz geordneten events-Bucket
// und filtert per MatchSubject — verwendet für die Wurzel-Abfrage ("/").
func scanEventsRecursive(tx *bolt.Tx, query string, opts ReadOptions, fn func(event.Event) bool) error {
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
		if err := unmarshalStored(v, &ev); err != nil {
			return fmt.Errorf("event dekodieren: %w", err)
		}
		if MatchSubject(ev.Subject, query, true) && matchType(ev.Type, types) {
			if !fn(ev) {
				return nil
			}
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
