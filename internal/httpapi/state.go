package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// stateResponse ist die gefaltete Zustandssicht eines Subjects (ADR-039). `state`
// ist das per Last-Write-Wins-Deep-Merge über die `data`-Payloads aller Events des
// Subjects entstandene Objekt; die übrigen Felder beschreiben die Herkunft (welche
// Events die Sicht erzeugt haben). Bewusst eine Komfort-Projektion über EIN
// Subject — kein materialisiertes Read-Model (das bleibt extern, CQRS).
type stateResponse struct {
	Subject       string         `json:"subject"`
	State         map[string]any `json:"state"`
	Revision      string         `json:"revision"`
	EventCount    uint64         `json:"eventCount"`
	FirstEventID  string         `json:"firstEventId"`
	LastEventID   string         `json:"lastEventId"`
	LastEventType string         `json:"lastEventType"`
	LastEventTime string         `json:"lastEventTime"`
	// At ist die obere Event-ID-Grenze, falls die Sicht auf einen historischen
	// Stand eingeschränkt wurde (`?at=`); sonst leer ("aktueller Stand").
	At string `json:"at,omitempty"`
}

// handleState liefert den gefalteten aktuellen Zustand EINES Subjects (ADR-039):
// die `data`-Payloads aller Events des Subjects werden in Schreibreihenfolge per
// Last-Write-Wins-Deep-Merge zu einem Objekt verschmolzen. So bekommt ein Client
// die aktuelle Sicht auf eine Entität, ohne selbst die Event-Historie falten zu
// müssen — der häufige „was ist der jetzige Stand?"-Fall.
//
// Bewusst NICHT rekursiv: ein Subject = ein Aggregat = ein Stream (ADR-005). Eine
// Teilbaum-Aggregation über mehrere Subjects ist eine andere, mehrdeutige Frage
// und bleibt einem nachgelagerten Read-Model vorbehalten (ADR-029/036).
//
// Optionen als Query-Parameter:
//   - at=<eventId>   — Zustand „as of" dieser (inklusiven) Event-ID rekonstruieren
//     (Zeitreise: der Stand, wie er nach diesem Event war). Default: aktueller Stand.
//   - type=<typ>     — nur Events dieses Typs in den Fold einbeziehen (wiederholbar).
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

	acc := map[string]any{}
	var (
		count                                    uint64
		firstID, lastID, lastType, lastTime, rev string
	)
	opts := store.ReadOptions{UpperBound: upper, Types: types}
	readErr := s.store.ReadFunc(subject, false, opts, func(ev event.Event) bool {
		if firstID == "" {
			firstID = ev.ID
		}
		lastID, lastType, lastTime, rev = ev.ID, ev.Type, ev.Time, ev.ID
		count++
		mergeState(acc, ev.Data)
		return true
	})
	if readErr != nil {
		s.logger.Error("state lesen fehlgeschlagen", "err", readErr, "subject", subject)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	if count == 0 {
		// Kein Event auf diesem Subject (ggf. im gewählten at/type-Fenster) → das
		// Subject hat keinen Zustand. 404 trennt sauber „leeres Objekt" von „nicht
		// vorhanden".
		writeError(w, http.StatusNotFound, "kein zustand für subject "+subject+" (keine passenden events)")
		return
	}

	writeJSON(w, http.StatusOK, stateResponse{
		Subject:       subject,
		State:         acc,
		Revision:      rev,
		EventCount:    count,
		FirstEventID:  firstID,
		LastEventID:   lastID,
		LastEventType: lastType,
		LastEventTime: lastTime,
		At:            q.Get("at"),
	})
}

// mergeState wendet die `data`-Payload eines Events per Last-Write-Wins-Deep-Merge
// auf den Akkumulator an (ADR-039). Konventionen:
//   - Objekte werden rekursiv pro Schlüssel verschmolzen.
//   - Skalare, Arrays und Typwechsel ersetzen den bisherigen Wert vollständig.
//   - JSON `null` als Wert ist ein Tombstone: der Schlüssel wird gelöscht (so kann
//     ein späteres Event ein Feld bewusst „zurücknehmen").
//   - Ist `data` leer oder kein JSON-Objekt (Array/Skalar), trägt es nichts zur
//     Feld-Sicht bei (das Event zählt aber weiter und bewegt die Metadaten).
func mergeState(acc map[string]any, data json.RawMessage) {
	if len(data) == 0 {
		return
	}
	var patch map[string]any
	if err := json.Unmarshal(data, &patch); err != nil {
		// Kein JSON-Objekt → für die Feld-Sicht ignoriert (bewusst, dokumentiert).
		return
	}
	deepMergeInto(acc, patch)
}

// deepMergeInto verschmilzt patch in acc (siehe mergeState für die Semantik).
func deepMergeInto(acc, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(acc, k) // Tombstone
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			if existing, ok := acc[k].(map[string]any); ok {
				deepMergeInto(existing, sub)
				continue
			}
			nested := map[string]any{}
			deepMergeInto(nested, sub)
			acc[k] = nested
			continue
		}
		acc[k] = v
	}
}
