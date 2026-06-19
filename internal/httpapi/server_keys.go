package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/clio/internal/auth"
)

// Admin-Routen zur Laufzeit-Verwaltung des Schlüsselbunds (ADR-025). Alle drei
// laufen unter requireScope(admin). Geheimnisse werden nur beim Anlegen einmalig
// im Klartext zurückgegeben (kid.secret) und nie wieder; die Liste enthält
// niemals Hashes oder Klartext.

// keyView ist die öffentliche Sicht auf einen Schlüssel — ohne secretHash.
type keyView struct {
	KID       string       `json:"kid"`
	Name      string       `json:"name"`
	Scopes    []auth.Scope `json:"scopes"`
	Status    auth.Status  `json:"status"`
	CreatedAt time.Time    `json:"createdAt"`
	RevokedAt *time.Time   `json:"revokedAt"`
}

func toKeyView(k auth.Key) keyView {
	return keyView{
		KID:       k.KID,
		Name:      k.Name,
		Scopes:    k.Scopes,
		Status:    k.Status,
		CreatedAt: k.CreatedAt,
		RevokedAt: k.RevokedAt,
	}
}

// createKeyRequest ist der Body von POST /api/v1/keys.
type createKeyRequest struct {
	Name   string       `json:"name"`
	Scopes []auth.Scope `json:"scopes"`
}

// handleCreateKey legt einen neuen Schlüssel an und antwortet EINMALIG mit dem
// vollständigen Klartextwert kid.secret (201). Der Wert ist danach nicht mehr
// abrufbar (nur sein Hash wird gespeichert).
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name ist pflicht")
		return
	}
	if len(req.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, "scopes darf nicht leer sein")
		return
	}
	for _, sc := range req.Scopes {
		if !sc.Valid() {
			writeError(w, http.StatusBadRequest, "unbekannter scope "+string(sc)+" (erlaubt: read, write, admin)")
			return
		}
	}

	key, secret, err := auth.GenerateKey(req.Name, req.Scopes)
	if err != nil {
		s.logger.Error("key erzeugen fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim erzeugen")
		return
	}
	if err := s.store.PutKey(key); err != nil {
		s.logger.Error("key speichern fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim speichern")
		return
	}

	byKID := ""
	if id, ok := identityFromContext(r); ok {
		byKID = id.KID
		s.logger.Info("key angelegt", "by", id.KID, "kid", key.KID, "name", key.Name, "scopes", key.Scopes)
	}
	s.emitKeyCreated(key, byKID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"kid":       key.KID,
		"name":      key.Name,
		"scopes":    key.Scopes,
		"status":    key.Status,
		"createdAt": key.CreatedAt,
		// Vollständiger Leitungswert — nur jetzt verfügbar.
		"secret":  key.KID + "." + secret,
		"warning": "Dieser Wert (kid.secret) wird nur einmal angezeigt und ist danach nicht mehr abrufbar. Jetzt sicher speichern.",
	})
}

// handleListKeys liefert alle Schlüssel ohne Geheimnisse/Hashes. Zusätzlich die
// Anzahl aktiver Admin-Keys und — wenn nur noch einer aktiv ist — eine Warnung
// (Self-Lockout-Schutz, kein harter Block).
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListKeys()
	if err != nil {
		s.logger.Error("keys auflisten fehlgeschlagen", "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim lesen")
		return
	}
	views := make([]keyView, 0, len(keys))
	for _, k := range keys {
		views = append(views, toKeyView(k))
	}
	activeAdmins := countActiveAdmins(keys)

	body := map[string]any{
		"keys":            views,
		"activeAdminKeys": activeAdmins,
	}
	if activeAdmins == 1 {
		body["warning"] = "Es ist nur noch ein aktiver Admin-Key vorhanden. Wird er widerrufen, ist keine Schlüsselverwaltung mehr möglich (Self-Lockout)."
	}
	writeJSON(w, http.StatusOK, body)
}

// handleRevokeKey widerruft einen Schlüssel (200) bzw. liefert 404 bei
// unbekanntem kid. Ein Admin darf sich auch selbst widerrufen — würde dadurch
// kein aktiver Admin-Key mehr übrig bleiben, enthält die Antwort eine Warnung
// (kein harter Block, um nicht handlungsunfähig zu werden).
func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	kid := r.PathValue("kid")
	if kid == "" {
		writeError(w, http.StatusBadRequest, "kid ist pflicht")
		return
	}

	found, err := s.store.RevokeKey(kid)
	if err != nil {
		s.logger.Error("key widerrufen fehlgeschlagen", "kid", kid, "err", err)
		writeError(w, http.StatusInternalServerError, "interner fehler beim widerrufen")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "unbekannter kid")
		return
	}

	byKID := ""
	if id, ok := identityFromContext(r); ok {
		byKID = id.KID
		s.logger.Warn("key widerrufen", "by", id.KID, "kid", kid)
	}
	s.emitKeyRevoked(kid, byKID)

	body := map[string]any{"kid": kid, "status": auth.StatusRevoked}
	// Nach dem Widerruf prüfen, ob noch ein aktiver Admin-Key existiert.
	if keys, err := s.store.ListKeys(); err == nil && countActiveAdmins(keys) == 0 {
		body["warning"] = "Es ist kein aktiver Admin-Key mehr vorhanden. Lege umgehend einen neuen Key an (z. B. via Bootstrap-Neustart), sonst ist keine Schlüsselverwaltung mehr möglich."
	}
	writeJSON(w, http.StatusOK, body)
}

// countActiveAdmins zählt die aktiven Schlüssel mit admin-Scope.
func countActiveAdmins(keys []auth.Key) int {
	n := 0
	for _, k := range keys {
		if k.Active() && k.HasScope(auth.ScopeAdmin) {
			n++
		}
	}
	return n
}
