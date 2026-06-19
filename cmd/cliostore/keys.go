package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/store"
)

// Offline-Schlüsselverwaltung auf der DB-Datei (ADR-025). Anders als die
// HTTP-Admin-Routen läuft diese CLI ohne laufende Instanz (sie öffnet die DB
// selbst, benötigt also einen gestoppten Server) — gedacht für Bootstrap und den
// Notfall/Recovery-Fall, wenn kein nutzbarer Admin-Key mehr existiert (Lockout).

// runKeys verteilt auf die Unterkommandos von `cliostore keys`.
func runKeys(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cliostore keys <list|create|rotate|revoke> [flags]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runKeysList(rest, out)
	case "create":
		return runKeysCreate(rest, out)
	case "rotate":
		return runKeysRotate(rest, out)
	case "revoke":
		return runKeysRevoke(rest, out)
	default:
		return fmt.Errorf("unbekanntes keys-kommando %q (list|create|rotate|revoke)", sub)
	}
}

// openStoreForCLI öffnet die DB für eine Offline-Verwaltungsaktion. Schlägt fehl,
// wenn eine laufende Instanz die Datei hält (Datei-Lock) — dann ist die
// HTTP-Admin-API der richtige Weg.
func openStoreForCLI(db string) (*store.Store, error) {
	st, err := store.OpenWithOptions(db, store.Options{})
	if err != nil {
		return nil, fmt.Errorf("db öffnen (läuft eine instanz? dann die HTTP-Admin-API nutzen): %w", err)
	}
	return st, nil
}

func runKeysList(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("keys list", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "Pfad zur Datenbank")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStoreForCLI(*db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	keys, err := st.ListKeys()
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	if *asJSON {
		// Bewusst ohne SecretHash: nur die nicht-geheime Sicht ausgeben.
		views := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			views = append(views, keyToPublicMap(k, now))
		}
		return writeJSONLine(out, map[string]any{"keys": views, "activeAdminKeys": activeAdmins(keys, now)})
	}

	if len(keys) == 0 {
		fmt.Fprintln(out, "keine schlüssel im bund")
		return nil
	}
	fmt.Fprintf(out, "%-16s %-20s %-18s %-10s %s\n", "KID", "NAME", "SCOPES", "STATUS", "EXPIRES")
	for _, k := range keys {
		status := string(k.Status)
		if k.Expired(now) {
			status = "expired"
		}
		fmt.Fprintf(out, "%-16s %-20s %-18s %-10s %s\n",
			k.KID, truncate(k.Name, 20), scopesString(k.Scopes), status, expiresString(k.ExpiresAt))
	}
	if n := activeAdmins(keys, now); n <= 1 {
		fmt.Fprintf(out, "\nhinweis: %d nutzbare(r) admin-key(s) — Lockout-Risiko, falls der letzte verloren geht\n", n)
	}
	return nil
}

func runKeysCreate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("keys create", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "Pfad zur Datenbank")
	name := fs.String("name", "", "Name des Schlüssels (Pflicht)")
	scopesCSV := fs.String("scopes", "", "kommagetrennte Scopes: read,write,admin (Pflicht)")
	expires := fs.String("expires", "", "optionales Ablaufdatum: Dauer (z. B. 720h) oder RFC3339-Zeitstempel")
	owner := fs.String("owner", "", "optionaler Owner")
	purpose := fs.String("purpose", "", "optionaler Zweck")
	description := fs.String("description", "", "optionale Beschreibung")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name ist erforderlich")
	}
	scopes, err := parseScopes(*scopesCSV)
	if err != nil {
		return err
	}
	meta := auth.KeyMeta{Description: *description, Owner: *owner, Purpose: *purpose}
	if *expires != "" {
		t, perr := parseExpiry(*expires)
		if perr != nil {
			return perr
		}
		meta.ExpiresAt = &t
	}

	st, err := openStoreForCLI(*db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	key, secret, err := auth.GenerateKeyWithMeta(*name, scopes, meta)
	if err != nil {
		return err
	}
	if err := st.PutKey(key); err != nil {
		return err
	}
	recordCLIKeyAudit(st, "key.create", key.KID)
	wire := key.KID + "." + secret

	if *asJSON {
		body := keyToPublicMap(key, time.Now().UTC())
		body["secret"] = wire
		return writeJSONLine(out, body)
	}
	fmt.Fprintf(out, "key angelegt: kid=%s scopes=%s%s\n", key.KID, scopesString(scopes), expiresSuffix(key.ExpiresAt))
	fmt.Fprintf(out, "secret (nur jetzt sichtbar): %s\n", wire)
	return nil
}

func runKeysRotate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("keys rotate", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "Pfad zur Datenbank")
	kid := fs.String("kid", "", "kid des zu rotierenden Schlüssels (Pflicht)")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kid == "" {
		return fmt.Errorf("--kid ist erforderlich")
	}
	st, err := openStoreForCLI(*db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	wire, found, err := st.RotateKey(*kid)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("unbekannter kid %q", *kid)
	}
	recordCLIKeyAudit(st, "key.rotate", *kid)
	if *asJSON {
		return writeJSONLine(out, map[string]any{"kid": *kid, "secret": wire})
	}
	fmt.Fprintf(out, "key rotiert: kid=%s — alter wert ungültig\n", *kid)
	fmt.Fprintf(out, "secret (nur jetzt sichtbar): %s\n", wire)
	return nil
}

func runKeysRevoke(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("keys revoke", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "Pfad zur Datenbank")
	kid := fs.String("kid", "", "kid des zu widerrufenden Schlüssels (Pflicht)")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kid == "" {
		return fmt.Errorf("--kid ist erforderlich")
	}
	st, err := openStoreForCLI(*db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	found, err := st.RevokeKey(*kid)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("unbekannter kid %q", *kid)
	}
	recordCLIKeyAudit(st, "key.revoke", *kid)
	if *asJSON {
		return writeJSONLine(out, map[string]any{"kid": *kid, "status": string(auth.StatusRevoked)})
	}
	fmt.Fprintf(out, "key widerrufen: kid=%s\n", *kid)
	return nil
}

// recordCLIKeyAudit schreibt einen Audit-Eintrag (ADR-031) für eine
// Offline-CLI-Key-Aktion (Actor "cli"). Best effort: ein Fehler bricht die Aktion
// nicht ab (auf stderr nicht nötig — die Aktion selbst ist bereits erfolgt).
func recordCLIKeyAudit(st *store.Store, action, kid string) {
	_ = st.AppendAudit(store.AuditEntry{Action: action, ActorName: "cli", Target: kid})
}

// parseScopes zerlegt eine kommagetrennte Scope-Liste und validiert jeden Eintrag.
func parseScopes(csv string) ([]auth.Scope, error) {
	parts := strings.Split(csv, ",")
	var scopes []auth.Scope
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		sc := auth.Scope(p)
		if !sc.Valid() {
			return nil, fmt.Errorf("unbekannter scope %q (erlaubt: read, write, admin, audit)", p)
		}
		scopes = append(scopes, sc)
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("--scopes ist erforderlich (z. B. read,write)")
	}
	return scopes, nil
}

// parseExpiry akzeptiert entweder eine Go-Dauer (relativ zu jetzt, z. B. 720h)
// oder einen absoluten RFC3339-Zeitstempel.
func parseExpiry(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--expires: weder Dauer (z. B. 720h) noch RFC3339-Zeitstempel: %q", s)
}

// keyToPublicMap baut die nicht-geheime Sicht eines Schlüssels (ohne SecretHash).
func keyToPublicMap(k auth.Key, now time.Time) map[string]any {
	m := map[string]any{
		"kid":       k.KID,
		"name":      k.Name,
		"scopes":    k.Scopes,
		"status":    k.Status,
		"createdAt": k.CreatedAt,
		"expired":   k.Expired(now),
	}
	if k.ExpiresAt != nil {
		m["expiresAt"] = k.ExpiresAt
	}
	if k.RevokedAt != nil {
		m["revokedAt"] = k.RevokedAt
	}
	if k.Description != "" {
		m["description"] = k.Description
	}
	if k.Owner != "" {
		m["owner"] = k.Owner
	}
	if k.Purpose != "" {
		m["purpose"] = k.Purpose
	}
	return m
}

func activeAdmins(keys []auth.Key, now time.Time) int {
	n := 0
	for _, k := range keys {
		if k.Usable(now) && k.HasScope(auth.ScopeAdmin) {
			n++
		}
	}
	return n
}

func scopesString(scopes []auth.Scope) string {
	parts := make([]string, len(scopes))
	for i, sc := range scopes {
		parts[i] = string(sc)
	}
	return strings.Join(parts, ",")
}

func expiresString(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format(time.RFC3339)
}

func expiresSuffix(t *time.Time) string {
	if t == nil {
		return ""
	}
	return " expires=" + t.UTC().Format(time.RFC3339)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
