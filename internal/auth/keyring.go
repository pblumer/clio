// Package auth ist das reine Domänenmodell für den persistenten Schlüsselbund
// (Keyring) von cliostore: benannte API-Keys mit Scopes, Status und Widerruf
// (ADR-025). Das Paket ist bewusst frei von Storage- und HTTP-Abhängigkeiten —
// es kennt weder bbolt noch net/http. Persistenz (internal/store) und Transport
// (internal/httpapi) bauen darauf auf.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Scope ist eine Berechtigung, die ein Schlüssel tragen kann. Routen verlangen
// jeweils genau einen Scope (siehe ADR-025, Scope-Mapping der Routen).
type Scope string

const (
	// ScopeRead erlaubt lesende Routen (read-events, observe, run-query, …).
	ScopeRead Scope = "read"
	// ScopeWrite erlaubt schreibende Datenrouten (write-events, register-schema).
	ScopeWrite Scope = "write"
	// ScopeAdmin erlaubt die Schlüsselverwaltung und Dev-Routen.
	ScopeAdmin Scope = "admin"
	// ScopeAudit erlaubt ausschließlich das read-only Lesen des Audit-Logs
	// (ADR-032). Bewusst getrennt von admin, damit ein reiner Auditor keine
	// administrativen Rechte braucht (Prinzip der geringsten Rechte).
	ScopeAudit Scope = "audit"
)

// Valid meldet, ob s einer der bekannten Scopes ist.
func (s Scope) Valid() bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeAdmin, ScopeAudit:
		return true
	}
	return false
}

// Status ist der Lebenszyklus-Zustand eines Schlüssels. Widerruf ist ein
// Status-Wechsel, kein Löschen — so bleibt die Zuordnung eines kid im Audit
// dauerhaft möglich (ADR-025).
type Status string

const (
	// StatusActive: der Schlüssel ist gültig und darf authentifizieren.
	StatusActive Status = "active"
	// StatusRevoked: der Schlüssel ist widerrufen und wird abgelehnt (401).
	StatusRevoked Status = "revoked"
)

// Key ist ein benannter API-Schlüssel. Persistiert wird ausschließlich der
// SHA-256-Hash des Geheimnisses (hex), niemals der Klartext. Die Felder ab
// ExpiresAt sind optionale Lebenszyklus-/Inventar-Metadaten (ADR-025): sie sind
// rückwärtskompatibel (omitempty) — ältere, ohne sie gespeicherte Keys laden
// unverändert (Zero-Werte: kein Ablauf, keine Beschreibung).
type Key struct {
	KID        string     `json:"kid"`
	Name       string     `json:"name"`
	SecretHash string     `json:"secretHash"`
	Scopes     []Scope    `json:"scopes"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	// ExpiresAt ist ein optionales Ablaufdatum. Ist es gesetzt und erreicht/
	// überschritten, gilt der Schlüssel als nicht mehr verwendbar (siehe Usable),
	// ohne dass er widerrufen werden muss. nil = kein Ablauf.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// Description/Owner/Purpose sind freie Inventar-Felder für den Betrieb
	// (wer/wofür) — rein dokumentarisch, ohne Sicherheitswirkung.
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
}

// KeyMeta bündelt die optionalen Metadaten beim Anlegen eines Schlüssels. Alle
// Felder sind optional; der Zero-Wert verhält sich byte-identisch zum bisherigen
// Anlegen ohne Metadaten.
type KeyMeta struct {
	Description string
	Owner       string
	Purpose     string
	ExpiresAt   *time.Time
}

// Active meldet, ob der Schlüssel-Status active ist (Status-Sicht, ohne Ablauf).
func (k Key) Active() bool { return k.Status == StatusActive }

// Expired meldet, ob der Schlüssel zum Zeitpunkt now abgelaufen ist. Ohne
// gesetztes ExpiresAt läuft ein Schlüssel nie ab. Der Ablauf ist inklusiv: genau
// zum ExpiresAt-Zeitpunkt gilt der Schlüssel bereits als abgelaufen.
func (k Key) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// Usable meldet, ob der Schlüssel zum Zeitpunkt now zur Authentifizierung taugt:
// aktiv (nicht widerrufen) UND nicht abgelaufen.
func (k Key) Usable(now time.Time) bool {
	return k.Active() && !k.Expired(now)
}

// HasScope meldet, ob der Schlüssel den geforderten (global notierten) Scope
// exakt trägt. Für admin/audit (immer global) ist das die richtige Prüfung; für
// subject-gebundene read/write-Berechtigungen siehe HasAction/Allows (ADR-033).
func (k Key) HasScope(s Scope) bool {
	for _, have := range k.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

// Grant ist ein aus einem Scope-String geparster Berechtigungseintrag (ADR-033).
// Ein Grant ist entweder global (Subject == "") oder auf einen Subject-Teilbaum
// eingeschränkt. admin/audit sind immer global.
type Grant struct {
	Action    Scope
	Subject   string // "" = global; sonst Präfix-Pfad (mit "/" beginnend, ohne Trailing-Slash)
	Recursive bool   // true bei "/*"-Suffix (ganzer Teilbaum); false = exaktes Subject
}

// Global meldet, ob der Grant nicht subject-gebunden ist (gilt für alle Subjects).
func (g Grant) Global() bool { return g.Subject == "" }

// ParseGrant zerlegt einen Scope-String in einen Grant (ADR-033):
//
//	"read"            -> global read
//	"read:/orders"    -> read, exaktes Subject /orders
//	"read:/orders/*"  -> read, Teilbaum unter /orders
//	"read:/*"         -> read, gesamter Baum (= effektiv global)
//
// admin/audit sind immer global und dürfen keinen ":"-Teil tragen. Nur read/write
// sind subject-gebunden. "*" ist ausschließlich als "/*"-Suffix erlaubt.
func ParseGrant(raw string) (Grant, error) {
	action, rest, bound := strings.Cut(raw, ":")
	a := Scope(action)
	switch a {
	case ScopeRead, ScopeWrite, ScopeAdmin, ScopeAudit:
	default:
		return Grant{}, fmt.Errorf("unbekannter scope %q (erlaubt: read, write, admin, audit; read/write optional subject-gebunden)", raw)
	}
	if !bound {
		return Grant{Action: a}, nil
	}
	if a != ScopeRead && a != ScopeWrite {
		return Grant{}, fmt.Errorf("scope %q: nur read/write sind subject-gebunden", raw)
	}
	pattern := rest
	recursive := false
	if strings.HasSuffix(pattern, "/*") {
		recursive = true
		pattern = strings.TrimSuffix(pattern, "/*")
		if pattern == "" {
			pattern = "/" // "/*" = ganzer Baum
		}
	}
	if pattern == "" || pattern[0] != '/' {
		return Grant{}, fmt.Errorf("scope %q: subject muss mit \"/\" beginnen", raw)
	}
	if strings.Contains(pattern, "*") {
		return Grant{}, fmt.Errorf("scope %q: \"*\" ist nur als \"/*\"-Suffix erlaubt", raw)
	}
	if len(pattern) > 1 {
		pattern = strings.TrimRight(pattern, "/") // Trailing-Slash normalisieren (außer Root)
	}
	return Grant{Action: a, Subject: pattern, Recursive: recursive}, nil
}

// ValidScopeString meldet einen Fehler, wenn raw kein gültiger Scope-String ist
// (global oder subject-gebunden, ADR-033). Genutzt bei der Schlüssel-Anlage.
func ValidScopeString(raw string) error {
	_, err := ParseGrant(raw)
	return err
}

// HasAction meldet, ob der Schlüssel mindestens einen Grant der Aktion trägt
// (global oder subject-gebunden) — das Aktions-Gate der Middleware (ADR-033).
// Ungültige Scope-Strings werden ignoriert (bei validierter Anlage kommen sie
// nicht vor).
func (k Key) HasAction(action Scope) bool {
	for _, raw := range k.Scopes {
		if g, err := ParseGrant(string(raw)); err == nil && g.Action == action {
			return true
		}
	}
	return false
}

// Allows meldet, ob der Schlüssel die Aktion auf dem angefragten Subject-Zugriff
// (subject, recursive) erlaubt (ADR-033). Delegiert an ScopesAllow.
func (k Key) Allows(action Scope, subject string, recursive bool) bool {
	return ScopesAllow(k.Scopes, action, subject, recursive)
}

// ScopesAllow meldet, ob die Scope-Liste die Aktion auf dem angefragten
// Subject-Zugriff erlaubt: ein globaler Grant erlaubt alles; ein subject-
// gebundener nur, wenn der angefragte Zugriff vollständig in seinem Teilbaum liegt.
func ScopesAllow(scopes []Scope, action Scope, subject string, recursive bool) bool {
	for _, raw := range scopes {
		g, err := ParseGrant(string(raw))
		if err != nil || g.Action != action {
			continue
		}
		if grantCovers(g, subject, recursive) {
			return true
		}
	}
	return false
}

// grantCovers meldet, ob der Grant den angefragten (subject, recursive)-Zugriff
// vollständig abdeckt.
func grantCovers(g Grant, subject string, recursive bool) bool {
	if g.Global() {
		return true
	}
	if recursive {
		// Die Anfrage berührt subject samt aller Nachfahren: nur ein rekursiver
		// Grant über einen Vorfahren-oder-gleichen Knoten deckt das vollständig ab.
		return g.Recursive && subtreeContains(g.Subject, subject)
	}
	if g.Recursive {
		return subtreeContains(g.Subject, subject)
	}
	return g.Subject == subject
}

// subtreeContains meldet, ob q == p oder q ein Nachfahre von p im Subject-Baum ist.
// Geschwister wie /booksstore zu /books werden korrekt ausgeschlossen (der
// "/"-Suffix-Vergleich verhindert die Präfix-Kollision); Root "/" deckt alles ab.
func subtreeContains(p, q string) bool {
	if p == "/" {
		return true
	}
	return q == p || strings.HasPrefix(q, p+"/")
}

// Identity ist die schlanke Sicht auf einen authentifizierten Aufrufer, die in
// den Request-Context und ins Audit-Log fließt (kein Geheimnis enthalten).
type Identity struct {
	KID    string
	Name   string
	Scopes []Scope
}

// bearerPrefix ist das Schema-Präfix des Authorization-Headers (case-insensitiv
// laut RFC 6750, daher per EqualFold verglichen).
const bearerPrefix = "Bearer "

// ParseBearer zerlegt einen `Authorization: Bearer kid.secret`-Header in seine
// Bestandteile. Getrennt wird am *ersten* Punkt — das Geheimnis darf selbst
// Punkte enthalten. ok ist false bei jedem Fehlformat (kein Bearer-Schema, kein
// Punkt, leerer kid oder leeres secret).
func ParseBearer(header string) (kid, secret string, ok bool) {
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", "", false
	}
	token := header[len(bearerPrefix):]
	i := strings.IndexByte(token, '.')
	if i <= 0 || i >= len(token)-1 {
		// Kein Punkt, führender Punkt (leerer kid) oder abschließender Punkt
		// (leeres secret) → Fehlformat.
		return "", "", false
	}
	return token[:i], token[i+1:], true
}

// HashSecret bildet ein Geheimnis auf seinen hex-kodierten SHA-256-Hash ab. Der
// Hash wird persistiert und zeitkonstant verglichen (siehe internal/httpapi).
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// idEncoding ist ein kleinbuchstabiges, padding-freies Base32-Alphabet für
// gut lesbare, URL-sichere kids und Geheimnisse.
var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

const (
	// kidPrefix kennzeichnet eine Schlüssel-ID auf einen Blick.
	kidPrefix = "kid_"
	// kidRandBytes ergibt 8 Base32-Zeichen (40 Bit) — kollisionsarm und kurz.
	kidRandBytes = 5
	// secretRandBytes sind 160 Bit Entropie für das Geheimnis (≥ Vorgabe).
	secretRandBytes = 20
)

// GenerateKey erzeugt einen neuen Schlüssel mit zufälligem kid und Geheimnis.
// Es wird nur der Hash des Geheimnisses im Record gespeichert; der zurückgegebene
// Klartext (das reine secret, ohne kid-Präfix) ist **einmalig** verfügbar und
// danach nicht mehr rekonstruierbar. Der vollständige Wert auf der Leitung ist
// `kid.secret` (siehe Key.KID).
func GenerateKey(name string, scopes []Scope) (Key, string, error) {
	return GenerateKeyWithMeta(name, scopes, KeyMeta{})
}

// GenerateKeyWithMeta erzeugt wie GenerateKey einen neuen Schlüssel, übernimmt
// aber zusätzlich die optionalen Metadaten (Ablauf, Beschreibung, Owner, Purpose).
func GenerateKeyWithMeta(name string, scopes []Scope, meta KeyMeta) (Key, string, error) {
	secret, err := randToken("", secretRandBytes)
	if err != nil {
		return Key{}, "", fmt.Errorf("secret erzeugen: %w", err)
	}
	k, err := NewKeyWithSecret(name, scopes, secret)
	if err != nil {
		return Key{}, "", err
	}
	k.applyMeta(meta)
	return k, secret, nil
}

// NewSecret erzeugt ein frisches Klartext-Geheimnis (ohne kid-Präfix) mit voller
// Entropie — genutzt beim Rotieren eines bestehenden Schlüssels (kid bleibt,
// Geheimnis wird ersetzt).
func NewSecret() (string, error) {
	return randToken("", secretRandBytes)
}

// applyMeta übernimmt die optionalen Metadaten in den Schlüssel (getrimmt).
func (k *Key) applyMeta(m KeyMeta) {
	k.Description = strings.TrimSpace(m.Description)
	k.Owner = strings.TrimSpace(m.Owner)
	k.Purpose = strings.TrimSpace(m.Purpose)
	k.ExpiresAt = m.ExpiresAt
}

// NewKeyWithSecret erzeugt einen Schlüssel mit zufälligem kid, aber einem vom
// Aufrufer vorgegebenen Klartext-Geheimnis (z. B. beim Bootstrap, wo der
// Betreiber das Geheimnis über eine ENV-Variable setzt). Gespeichert wird nur
// der Hash. Der vollständige Leitungswert ist k.KID + "." + secret.
func NewKeyWithSecret(name string, scopes []Scope, secret string) (Key, error) {
	kid, err := randToken(kidPrefix, kidRandBytes)
	if err != nil {
		return Key{}, fmt.Errorf("kid erzeugen: %w", err)
	}
	return Key{
		KID:        kid,
		Name:       name,
		SecretHash: HashSecret(secret),
		Scopes:     scopes,
		Status:     StatusActive,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// randToken liefert prefix + Base32-Kodierung von n Zufallsbytes.
func randToken(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + idEncoding.EncodeToString(b), nil
}
