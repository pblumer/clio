package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/clio/internal/store"
)

// Die Dev-Routen sind nur im Dev-Mode (CLIO_DEV_MODE, ADR-022) registriert; ohne
// ihn existieren sie gar nicht (404 statt nur 401, Defense in Depth). Sie sind
// zusätzlich scope-geschützt (admin). Siehe routes.go für die Registrierung.

// handleDevReset setzt die gesamte Datenbank zurück (Tabula rasa) und meldet, wie
// viele Events dabei „verglüht" sind. Diese Route existiert NUR im Dev-Mode
// (CLIO_DEV_MODE, ADR-022); im Produktivbetrieb ist sie nicht registriert. Sie
// ist trotzdem zusätzlich Bearer-Token-geschützt (Defense in Depth).
//
// Hinweis: Bereits laufende Observer-Streams werden nicht aktiv getrennt; da die
// Sequenz wieder bei 1 beginnt, liegen neue IDs unter ihrem zuletzt gesehenen
// Stand und sie liefern erst nach einem Reconnect wieder. Für ein Dev-Werkzeug
// ist das akzeptabel — das Dashboard verbindet seinen Stream beim nächsten
// „Verbinden" ohnehin neu.
func (s *Server) handleDevReset(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.store.Reset()
	if err != nil {
		s.logger.Error("dev-reset fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim zurücksetzen")
		return
	}

	now := time.Now().UTC()
	// Eventstrom-Histogramm zurücksetzen, damit der Chart ebenfalls bei null
	// startet (origin = jetzt).
	s.events.Reset(now)

	// Zustands-Cache leeren (ADR-040): nach dem Reset beginnt die Sequenz wieder
	// bei 1; gecachte Stände mit höherer lastSeq würden neue Events sonst nicht
	// inkrementell aufnehmen.
	s.stateCache.clear()

	// Nach jeder Supernova wieder Bulk-Import-Fenster öffnen.
	s.bulkMu.Lock()
	s.bulkImportOpen = true
	s.bulkMu.Unlock()

	s.logger.Warn("datenbank zurückgesetzt (dev-mode)", "deletedEvents", deleted)
	s.recordAudit(r, store.AuditActionDevReset, strconv.FormatUint(deleted, 10)+" events", "")

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "tabula-rasa",
		"deletedEvents":  deleted,
		"resetAt":        now.Format(time.RFC3339Nano),
		"bulkImportOpen": true,
		"message":        "Supernova! Die Historie ist zu Sternenstaub zerfallen.",
	})
}

// handleDevBulkImportEvents erlaubt Bulk-Import nur im expliziten Startfenster
// direkt nach Server-Start oder nach /api/v1/dev/reset-database.
// Sobald das Fenster geschlossen wurde, liefert diese Route 409.
func (s *Server) handleDevBulkImportEvents(w http.ResponseWriter, r *http.Request) {
	s.bulkMu.RLock()
	open := s.bulkImportOpen
	s.bulkMu.RUnlock()
	if !open {
		writeError(w, http.StatusConflict, "bulk-import-fenster ist geschlossen; führe erst dev/reset-database aus")
		return
	}

	// gleiche Semantik wie write-events, nur im dev-startfenster erlaubt.
	var req writeEventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "events darf nicht leer sein")
		return
	}
	for i, c := range req.Events {
		if err := c.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "events["+strconv.Itoa(i)+"]: "+err.Error())
			return
		}
	}
	preconditions, err := s.parsePreconditions(req.Preconditions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	written, err := s.store.AppendAuthored(req.Events, preconditions, s.authorKID(r))
	if err != nil {
		if errors.Is(err, store.ErrPreconditionFailed) {
			s.metrics.IncPreconditionFailure()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, store.ErrSchemaValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("dev bulk-import fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim schreiben")
		return
	}

	s.metrics.AddEventsWritten(len(written))
	s.recordEventStats(written)
	s.broker.Publish(written)
	writeNDJSON(w, s.logger, written)
}

// handleDevCloseBulkImport schließt das Startfenster explizit.
func (s *Server) handleDevCloseBulkImport(w http.ResponseWriter, r *http.Request) {
	s.bulkMu.Lock()
	s.bulkImportOpen = false
	s.bulkMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "closed",
		"bulkImportOpen": false,
		"closedAt":       time.Now().UTC().Format(time.RFC3339Nano),
	})
}
