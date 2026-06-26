package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/store"
)

// registerReduceSpecRequest ist der Body von POST /register-reduce-spec (ADR-041).
type registerReduceSpecRequest struct {
	Prefix string          `json:"prefix"`
	Spec   json.RawMessage `json:"spec"`
}

// handleRegisterReduceSpec registriert/überschreibt die Reduce-Spec eines Subject-
// Prefix. Reduce-Specs sind mutable Lese-Konfiguration und gelten prefix-weit →
// globaler `write` (wie Schema-Registrierung, ADR-033/014).
func (s *Server) handleRegisterReduceSpec(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGlobal(w, r, auth.ScopeWrite) {
		return
	}
	var req registerReduceSpecRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Prefix == "" || req.Prefix[0] != '/' {
		writeError(w, http.StatusBadRequest, "prefix muss mit \"/\" beginnen")
		return
	}
	if len(req.Spec) == 0 {
		writeError(w, http.StatusBadRequest, "spec ist pflicht")
		return
	}
	if err := s.store.RegisterReduceSpec(req.Prefix, req.Spec); err != nil {
		if errors.Is(err, store.ErrReduceSpecValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("register-reduce-spec fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim registrieren")
		return
	}
	s.recordAudit(r, store.AuditActionReduceSpecRegister, req.Prefix, "")
	writeJSON(w, http.StatusOK, map[string]string{"prefix": req.Prefix, "status": "registered"})
}

// handleReadReduceSpec liefert Reduce-Specs:
//   - ?prefix=/orders  → exakte Spec dieses Prefix (404, wenn keine)
//   - ?subject=/orders/1 → wirksame Spec für dieses Subject (längster Prefix; 404)
//   - ohne Parameter    → alle Specs als NDJSON
func (s *Server) handleReadReduceSpec(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGlobal(w, r, auth.ScopeRead) {
		return
	}
	q := r.URL.Query()
	switch {
	case q.Get("subject") != "":
		subject := q.Get("subject")
		if subject[0] != '/' {
			writeError(w, http.StatusBadRequest, "subject muss mit \"/\" beginnen")
			return
		}
		raw, prefix, found, err := s.store.ReduceSpecFor(subject)
		if err != nil {
			s.logger.Error("read-reduce-spec (subject) fehlgeschlagen", "err", err)
			writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "für dieses subject ist keine reduce-spec wirksam (default-lww)")
			return
		}
		writeJSON(w, http.StatusOK, store.ReduceSpecInfo{Prefix: prefix, Spec: raw})
	case q.Get("prefix") != "":
		prefix := q.Get("prefix")
		raw, found, err := s.reduceSpecExact(prefix)
		if err != nil {
			s.logger.Error("read-reduce-spec (prefix) fehlgeschlagen", "err", err)
			writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "für diesen prefix ist keine reduce-spec registriert")
			return
		}
		writeJSON(w, http.StatusOK, store.ReduceSpecInfo{Prefix: prefix, Spec: raw})
	default:
		specs, err := s.store.ReduceSpecs()
		if err != nil {
			s.logger.Error("read-reduce-spec (liste) fehlgeschlagen", "err", err)
			writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
			return
		}
		writeNDJSON(w, s.logger, specs)
	}
}

// reduceSpecExact liefert die Spec, die EXAKT unter prefix registriert ist (nicht
// die wirksame über die Prefix-Hierarchie).
func (s *Server) reduceSpecExact(prefix string) (json.RawMessage, bool, error) {
	specs, err := s.store.ReduceSpecs()
	if err != nil {
		return nil, false, err
	}
	for _, si := range specs {
		if si.Prefix == prefix {
			return si.Spec, true, nil
		}
	}
	return nil, false, nil
}

// handleDeleteReduceSpec entfernt die Spec eines Prefix (?prefix=…). Wie die
// Registrierung globaler `write`.
func (s *Server) handleDeleteReduceSpec(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGlobal(w, r, auth.ScopeWrite) {
		return
	}
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" || prefix[0] != '/' {
		writeError(w, http.StatusBadRequest, "query-parameter prefix muss mit \"/\" beginnen")
		return
	}
	found, err := s.store.DeleteReduceSpec(prefix)
	if err != nil {
		s.logger.Error("delete-reduce-spec fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim löschen")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "für diesen prefix ist keine reduce-spec registriert")
		return
	}
	s.recordAudit(r, store.AuditActionReduceSpecDelete, prefix, "")
	writeJSON(w, http.StatusOK, map[string]string{"prefix": prefix, "status": "deleted"})
}
