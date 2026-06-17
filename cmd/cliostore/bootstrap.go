package main

import (
	"fmt"
	"log/slog"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

// adminScopes ist der volle Scope-Satz eines Bootstrap-/Legacy-Admin-Keys.
var adminScopes = []auth.Scope{auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin}

// errNoAuthMaterial signalisiert, dass weder Schlüssel vorhanden sind noch
// Bootstrap-Material gesetzt ist — der Start wird verweigert.
var errNoAuthMaterial = fmt.Errorf(
	"kein Auth-Material: setze %s (oder das deprecated %s) oder lege vorab Keys an",
	"CLIO_BOOTSTRAP_ADMIN_KEY", "CLIO_API_TOKEN")

// bootstrapAuth stellt sicher, dass beim Start gültiges Auth-Material existiert
// (ADR-025). Verhalten:
//   - Schlüsselbund nicht leer: nichts tun (normaler Start).
//   - Bund leer + CLIO_BOOTSTRAP_ADMIN_KEY gesetzt: einen Admin-Key mit diesem
//     Klartext-Geheimnis anlegen (kid wird generiert).
//   - Bund leer + nur CLIO_API_TOKEN gesetzt: einen deprecated Legacy-Admin-Key
//     (Name "legacy-token") mit dem Token als Geheimnis anlegen. Variante (b):
//     der Leitungswert ist NUR `kid.secret`; ein altes `Bearer <token>` ohne
//     kid-Präfix wird nicht mehr akzeptiert (401), bis der Betreiber umstellt.
//   - Bund leer + nichts gesetzt: Start abbrechen (errNoAuthMaterial).
//
// Es wird höchstens EIN Schlüssel angelegt und nur bei leerem Bund. Das
// Klartext-Geheimnis stammt jeweils vom Betreiber (ENV) und wird NICHT geloggt
// (Sicherheits-Checkliste §3) — nur der generierte kid plus ein Hinweis, wie
// daraus der vollständige Wert `kid.secret` entsteht.
func bootstrapAuth(st *store.Store, cfg config.Config, logger *slog.Logger) error {
	n, err := st.CountKeys()
	if err != nil {
		return fmt.Errorf("schlüsselbund prüfen: %w", err)
	}
	if n > 0 {
		return nil // bereits Schlüssel vorhanden — kein Bootstrap
	}

	switch {
	case cfg.BootstrapAdminKey != "":
		k, err := auth.NewKeyWithSecret("bootstrap-admin", adminScopes, cfg.BootstrapAdminKey)
		if err != nil {
			return fmt.Errorf("bootstrap-admin-key erzeugen: %w", err)
		}
		if err := st.PutKey(k); err != nil {
			return fmt.Errorf("bootstrap-admin-key speichern: %w", err)
		}
		logger.Warn("BOOTSTRAP — initialen Admin-Key aus CLIO_BOOTSTRAP_ADMIN_KEY angelegt; "+
			"verwende den Header Authorization: Bearer <kid>.<CLIO_BOOTSTRAP_ADMIN_KEY> "+
			"und lege anschließend benannte Keys an; entferne danach die Bootstrap-Variable",
			"kid", k.KID, "name", k.Name, "scopes", k.Scopes)
		return nil

	case cfg.APIToken != "":
		k, err := auth.NewKeyWithSecret("legacy-token", adminScopes, cfg.APIToken)
		if err != nil {
			return fmt.Errorf("legacy-token-key erzeugen: %w", err)
		}
		if err := st.PutKey(k); err != nil {
			return fmt.Errorf("legacy-token-key speichern: %w", err)
		}
		logger.Warn("BOOTSTRAP — CLIO_API_TOKEN ist deprecated (ADR-025). Es wurde ein "+
			"Legacy-Admin-Key angelegt. Der Leitungswert ist jetzt kid.secret: verwende "+
			"Authorization: Bearer <kid>.<CLIO_API_TOKEN>. Das alte Format ohne kid-Präfix "+
			"wird nicht mehr akzeptiert; migriere auf benannte Keys",
			"kid", k.KID, "name", k.Name, "scopes", k.Scopes)
		return nil

	default:
		return errNoAuthMaterial
	}
}
