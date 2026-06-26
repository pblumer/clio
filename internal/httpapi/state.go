package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// stateResponse ist die gefaltete Zustandssicht eines Subjects (ADR-039). `state`
// ist das aus den `data`-Payloads gefaltete Objekt (Default: Last-Write-Wins-Deep-
// Merge; feldweise Strategien per Reduce-Spec, ADR-041). Die übrigen Felder
// beschreiben die Herkunft (welche Events die Sicht erzeugt haben).
type stateResponse struct {
	Subject       string         `json:"subject"`
	State         map[string]any `json:"state"`
	Revision      string         `json:"revision"`
	EventCount    uint64         `json:"eventCount"`
	FirstEventID  string         `json:"firstEventId"`
	LastEventID   string         `json:"lastEventId"`
	LastEventType string         `json:"lastEventType"`
	LastEventTime string         `json:"lastEventTime"`
	// Reducer ist der Subject-Prefix der wirksamen Reduce-Spec (ADR-041); leer =
	// Default-LWW (ADR-039).
	Reducer string `json:"reducer,omitempty"`
	// At ist die obere Event-ID-Grenze, falls die Sicht auf einen historischen
	// Stand eingeschränkt wurde (`?at=`); sonst leer ("aktueller Stand").
	At string `json:"at,omitempty"`
}

// handleState liefert den gefalteten aktuellen Zustand EINES Subjects (ADR-039):
// die `data`-Payloads aller Events des Subjects werden in Schreibreihenfolge zu
// einem Objekt verschmolzen. Default ist Last-Write-Wins-Deep-Merge; ist für das
// Subject eine Reduce-Spec registriert (ADR-041), greifen deren feldweise
// Strategien (sum/min/max/append/union/first).
//
// Bewusst NICHT rekursiv: ein Subject = ein Aggregat = ein Stream (ADR-005).
//
// Optionen als Query-Parameter:
//   - at=<eventId>   — Zustand „as of" dieser (inklusiven) Event-ID (Zeitreise).
//   - type=<typ>     — nur Events dieses Typs in den Fold einbeziehen (wiederholbar).
//
// Caching (ADR-040): die „nackte" Abfrage (ohne at/type) wird über einen
// In-Memory-LRU bedient und lazy-inkrementell ab der zuletzt gefalteten Event-ID
// fortgeschrieben. Abfragen mit at/type umgehen den Cache und falten frisch.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	// Subject aus dem Pfad: "orders/1" -> "/orders/1"; leer -> "/".
	subject := "/" + strings.TrimSuffix(r.PathValue("subject"), "/")

	// Zustand ist immer single-subject (nicht rekursiv) → exakter Subject-Grant.
	if !s.authorizeSubject(w, r, auth.ScopeRead, subject, false) {
		return
	}

	q := r.URL.Query()
	upper, err := parseBound(q.Get("at"), "at")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	types := q["type"]
	if err := validateTypes(types); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Wirksame Reduce-Spec ermitteln (längster passender Prefix, ADR-041). Fehlt
	// eine, bleibt es beim Default-LWW (ADR-039). Der kanonische Spec-Inhalt dient
	// als Cache-Fingerprint.
	rawSpec, specPrefix, _, err := s.store.ReduceSpecFor(subject)
	if err != nil {
		s.logger.Error("state: reduce-spec lesen fehlgeschlagen", "err", err, "subject", subject)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	rs, err := parseReduceSpec(rawSpec)
	if err != nil {
		// Eine gespeicherte Spec ist beim Registrieren validiert worden; ein Fehler
		// hier ist intern, nicht client-verschuldet.
		s.logger.Error("state: reduce-spec parsen fehlgeschlagen", "err", err, "prefix", specPrefix)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	fingerprint := string(rawSpec)

	cacheable := upper == 0 && len(types) == 0

	var entry stateEntry
	if cacheable {
		entry, err = s.foldCached(subject, fingerprint, rs)
	} else {
		entry, err = s.foldFresh(subject, store.ReadOptions{UpperBound: upper, Types: types}, rs)
	}
	if err != nil {
		s.logger.Error("state lesen fehlgeschlagen", "err", err, "subject", subject)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	if entry.count == 0 {
		writeError(w, http.StatusNotFound, "kein zustand für subject "+subject+" (keine passenden events)")
		return
	}

	writeJSON(w, http.StatusOK, stateResponse{
		Subject:       subject,
		State:         entry.state,
		Revision:      entry.lastID,
		EventCount:    entry.count,
		FirstEventID:  entry.firstID,
		LastEventID:   entry.lastID,
		LastEventType: entry.lastEventType,
		LastEventTime: entry.lastEventTime,
		Reducer:       specPrefix,
		At:            q.Get("at"),
	})
}

// foldFresh faltet den Zustand eines Subjects vollständig neu (kein Cache) — für
// at/type-Abfragen. Der zurückgegebene state ist exklusiv für den Aufrufer.
func (s *Server) foldFresh(subject string, opts store.ReadOptions, rs *reduceSpec) (stateEntry, error) {
	e := stateEntry{state: map[string]any{}}
	err := s.store.ReadFunc(subject, false, opts, func(ev event.Event) bool {
		foldInto(&e, ev, rs)
		return true
	})
	return e, err
}

// foldCached bedient die nackte Abfrage über den LRU (ADR-040): trifft der Cache
// (und passt der Fingerprint), wird nur die Differenz ab der zuletzt gefalteten
// Event-ID nachgefaltet; sonst wird vollständig gefaltet. Der zurückgegebene state
// ist eine Kopie (der gecachte Stand wird nicht mit der Antwort geteilt).
func (s *Server) foldCached(subject, fingerprint string, rs *reduceSpec) (stateEntry, error) {
	base, hit := s.stateCache.get(subject)
	if hit && base.fingerprint == fingerprint {
		// Inkrementell ab base.lastSeq+1 weiterfalten (auf einer Kopie).
		next := stateEntry{
			fingerprint:   fingerprint,
			state:         deepCopyMap(base.state),
			count:         base.count,
			firstID:       base.firstID,
			lastID:        base.lastID,
			lastSeq:       base.lastSeq,
			lastEventType: base.lastEventType,
			lastEventTime: base.lastEventTime,
		}
		opts := store.ReadOptions{LowerBound: base.lastSeq + 1}
		err := s.store.ReadFunc(subject, false, opts, func(ev event.Event) bool {
			foldInto(&next, ev, rs)
			return true
		})
		if err != nil {
			return stateEntry{}, err
		}
		s.stateCache.put(subject, next)
		return next, nil
	}

	// Cache-Miss oder veralteter Fingerprint → vollständig falten.
	fresh := stateEntry{fingerprint: fingerprint, state: map[string]any{}}
	err := s.store.ReadFunc(subject, false, store.ReadOptions{}, func(ev event.Event) bool {
		foldInto(&fresh, ev, rs)
		return true
	})
	if err != nil {
		return stateEntry{}, err
	}
	s.stateCache.put(subject, fresh)
	// Eine Kopie ausliefern, damit der gecachte Stand nicht mit der Antwort geteilt
	// wird (ein Folge-Request würde ihn sonst über deepCopy hinaus mutieren können).
	out := fresh
	out.state = deepCopyMap(fresh.state)
	return out, nil
}

// foldInto faltet ein einzelnes Event in den Akkumulator und führt die Metadaten
// fort (Reihenfolge: älteste → jüngste, vom Store garantiert).
func foldInto(e *stateEntry, ev event.Event, rs *reduceSpec) {
	if e.firstID == "" {
		e.firstID = ev.ID
	}
	e.lastID = ev.ID
	if seq, perr := strconv.ParseUint(ev.ID, 10, 64); perr == nil {
		e.lastSeq = seq
	}
	e.lastEventType = ev.Type
	e.lastEventTime = ev.Time
	e.count++
	applyEvent(e.state, ev.Data, rs)
}
