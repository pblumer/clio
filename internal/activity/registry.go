// Package activity ist die prozesslokale, nebenläufigkeitssichere Presence- und
// Aktivitäts-Registry von cliostore (ADR-030, WP-01). Sie beantwortet zwei
// Fragen: „wer ist gerade online?" und „wer tut was?" — aggregiert je
// authentifiziertem Schlüssel (kid, ADR-025).
//
// Bewusst in-memory und flüchtig: Presence ist Laufzeit-State wie Metriken, kein
// Event und keine Quelle der Wahrheit über die Vergangenheit. Nach einem
// Neustart ist die Registry leer; die dauerhafte Spur sind die optionalen
// Auth-Lifecycle-Events (CLIO_AUTH_EVENTS, /_clio/auth/…). Das passt zur
// Single-Instance-Annahme (ADR-002).
//
// Das Paket ist absichtlich abhängigkeitsfrei (nur Standardbibliothek): es kennt
// weder internal/store noch net/http noch internal/auth. Scopes werden als
// schlichte Strings geführt; der HTTP-Layer (internal/httpapi) übersetzt seine
// auth.Scope-Werte beim Aufruf.
package activity

import (
	"sort"
	"sync"
	"time"
)

// Category ordnet eine Anfrage einer der drei Scope-Klassen zu (abgeleitet aus
// dem Scope der aufgerufenen Route). Sie bestimmt, welcher Zähler eines
// Eintrags bei einer erfolgreichen Anfrage erhöht wird.
type Category int

const (
	// CategoryRead steht für lesende Routen (read-events, observe, run-query, …).
	CategoryRead Category = iota
	// CategoryWrite steht für schreibende Datenrouten (write-events, register-schema).
	CategoryWrite
	// CategoryAdmin steht für die Schlüsselverwaltung und Dev-Routen.
	CategoryAdmin
)

// Snapshot ist die nach außen sichtbare Momentaufnahme eines Schlüssels. Sie
// enthält ausschließlich nicht-geheime Felder — niemals ein Secret oder dessen
// Hash (ADR-030, Review-Gate).
type Snapshot struct {
	KID            string    `json:"kid"`
	Name           string    `json:"name"`
	Scopes         []string  `json:"scopes"`
	FirstSeen      time.Time `json:"firstSeen"`
	LastSeen       time.Time `json:"lastSeen"`
	Reads          uint64    `json:"reads"`
	Writes         uint64    `json:"writes"`
	AdminOps       uint64    `json:"adminOps"`
	Denied         uint64    `json:"denied"`
	OpenObserves   int       `json:"openObserves"`
	Online         bool      `json:"online"`
	SessionStarted time.Time `json:"sessionStarted,omitempty"`
}

// Ended beschreibt eine Presence-Session, die beim Sweep abgelaufen ist — der
// Auslöser für ein optionales session-ended-Event (WP-04).
type Ended struct {
	KID            string
	Name           string
	Scopes         []string
	SessionStarted time.Time
	LastSeen       time.Time
}

// entry ist der interne, veränderliche Zustand je Schlüssel.
type entry struct {
	name         string
	scopes       []string
	firstSeen    time.Time
	lastSeen     time.Time // Zeit der letzten ERFOLGREICHEN Aktivität (Basis fürs Online-Fenster)
	reads        uint64
	writes       uint64
	adminOps     uint64
	denied       uint64
	openObserves int
	// sessionStarted ist != zero, solange eine Presence-Session als laufend gilt.
	// Gesetzt beim Übergang offline→online, geleert vom Sweep am Session-Ende.
	sessionStarted time.Time
}

// Registry hält die Presence-/Aktivitätsdaten aller je gesehenen Schlüssel
// dieser Laufzeit. Alle Methoden sind nebenläufigkeitssicher.
type Registry struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string]*entry
}

// New erzeugt eine leere Registry. window ist das gleitende „Online"-Fenster:
// ein Schlüssel ohne offene Observe-Verbindung gilt als online, solange seine
// letzte erfolgreiche Aktivität jünger als window ist (CLIO_PRESENCE_WINDOW).
func New(window time.Duration) *Registry {
	return &Registry{
		window:  window,
		entries: make(map[string]*entry),
	}
}

// online meldet, ob e zum Zeitpunkt now als online gilt: entweder hält der
// Schlüssel mindestens eine offene Observe-Verbindung, oder seine letzte
// erfolgreiche Aktivität liegt innerhalb des Fensters. Erfordert gehaltenes mu.
func (r *Registry) online(e *entry, now time.Time) bool {
	if e.openObserves > 0 {
		return true
	}
	return !e.lastSeen.IsZero() && now.Sub(e.lastSeen) < r.window
}

// getOrCreate liefert den Eintrag für kid und legt ihn bei Bedarf an. Erfordert
// gehaltenes mu.
func (r *Registry) getOrCreate(kid string, now time.Time) *entry {
	e, ok := r.entries[kid]
	if !ok {
		e = &entry{firstSeen: now}
		r.entries[kid] = e
	}
	return e
}

// Record verbucht eine abgeschlossene Anfrage des Schlüssels kid. allowed
// unterscheidet zugelassene (200) von abgelehnten (401/403) Anfragen: nur
// zugelassene Anfragen zählen als Aktivität, halten den Online-Status und können
// eine Session starten; abgelehnte erhöhen ausschließlich den Denied-Zähler.
//
// Rückgabe sessionStarted ist true, wenn diese Anfrage einen Übergang
// offline→online ausgelöst hat — der Auslöser für ein optionales
// session-started-Event (WP-04).
//
// Hinweis für Aufrufer: Bei abgelehnten Anfragen sollte Record nur mit einem
// BEKANNTEN kid aufgerufen werden (z. B. 403 oder widerrufener Schlüssel), nicht
// mit beliebigen, unbekannten kids aus 401-Versuchen — sonst kann ein Angreifer
// die Registry mit Müll-Einträgen aufblähen.
func (r *Registry) Record(kid, name string, scopes []string, cat Category, allowed bool, now time.Time) (sessionStarted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e := r.getOrCreate(kid, now)
	e.name = name
	e.scopes = scopes

	if !allowed {
		e.denied++
		return false
	}

	// Übergang offline→online VOR der Aktualisierung von lastSeen bestimmen.
	if !r.online(e, now) {
		e.sessionStarted = now
		sessionStarted = true
	}

	e.lastSeen = now
	switch cat {
	case CategoryWrite:
		e.writes++
	case CategoryAdmin:
		e.adminOps++
	default:
		e.reads++
	}
	return sessionStarted
}

// OpenObserve verbucht den Beginn einer offenen Observe-Verbindung des
// Schlüssels kid (Live-Beobachtung = unzweifelhaft online). Rückgabe
// sessionStarted ist true bei einem Übergang offline→online.
func (r *Registry) OpenObserve(kid, name string, scopes []string, now time.Time) (sessionStarted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e := r.getOrCreate(kid, now)
	e.name = name
	e.scopes = scopes

	if !r.online(e, now) {
		e.sessionStarted = now
		sessionStarted = true
	}
	e.openObserves++
	e.lastSeen = now
	return sessionStarted
}

// CloseObserve verbucht das Ende einer offenen Observe-Verbindung des Schlüssels
// kid. Idempotent gegenüber einem unbekannten kid. lastSeen wird auf den
// Schließzeitpunkt gesetzt, damit der Schlüssel noch ein Fenster lang als online
// gilt (nicht abruptes „offline" beim Verbindungsabbruch).
func (r *Registry) CloseObserve(kid string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[kid]
	if !ok {
		return
	}
	if e.openObserves > 0 {
		e.openObserves--
	}
	e.lastSeen = now
}

// Snapshot liefert eine sortierte Momentaufnahme aller Schlüssel: online zuerst,
// danach nach letzter Aktivität (jüngste zuerst). SessionStarted wird nur für
// aktuell online stehende Schlüssel ausgegeben.
func (r *Registry) Snapshot(now time.Time) []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Snapshot, 0, len(r.entries))
	for kid, e := range r.entries {
		online := r.online(e, now)
		s := Snapshot{
			KID:          kid,
			Name:         e.name,
			Scopes:       append([]string(nil), e.scopes...),
			FirstSeen:    e.firstSeen,
			LastSeen:     e.lastSeen,
			Reads:        e.reads,
			Writes:       e.writes,
			AdminOps:     e.adminOps,
			Denied:       e.denied,
			OpenObserves: e.openObserves,
			Online:       online,
		}
		if online {
			s.SessionStarted = e.sessionStarted
		}
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online // online vor offline
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen) // jüngste zuerst
		}
		return out[i].KID < out[j].KID // stabile, deterministische Ordnung
	})
	return out
}

// Sweep schließt abgelaufene Presence-Sessions: für jeden Schlüssel mit einer
// laufenden Session (sessionStarted gesetzt), der zum Zeitpunkt now nicht mehr
// online ist, wird die Session beendet (Markierung geleert) und in der Rückgabe
// gemeldet. Die Rückgabe ist der Auslöser für optionale session-ended-Events
// (WP-04). Aufruf typischerweise periodisch aus einem Hintergrund-Ticker.
func (r *Registry) Sweep(now time.Time) []Ended {
	r.mu.Lock()
	defer r.mu.Unlock()

	var ended []Ended
	for kid, e := range r.entries {
		if e.sessionStarted.IsZero() || r.online(e, now) {
			continue
		}
		ended = append(ended, Ended{
			KID:            kid,
			Name:           e.name,
			Scopes:         append([]string(nil), e.scopes...),
			SessionStarted: e.sessionStarted,
			LastSeen:       e.lastSeen,
		})
		e.sessionStarted = time.Time{}
	}
	return ended
}
