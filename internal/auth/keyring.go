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
)

// Valid meldet, ob s einer der bekannten Scopes ist.
func (s Scope) Valid() bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeAdmin:
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
// SHA-256-Hash des Geheimnisses (hex), niemals der Klartext.
type Key struct {
	KID        string     `json:"kid"`
	Name       string     `json:"name"`
	SecretHash string     `json:"secretHash"`
	Scopes     []Scope    `json:"scopes"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// Active meldet, ob der Schlüssel aktuell zur Authentifizierung taugt.
func (k Key) Active() bool { return k.Status == StatusActive }

// HasScope meldet, ob der Schlüssel den geforderten Scope trägt.
func (k Key) HasScope(s Scope) bool {
	for _, have := range k.Scopes {
		if have == s {
			return true
		}
	}
	return false
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
	kid, err := randToken(kidPrefix, kidRandBytes)
	if err != nil {
		return Key{}, "", fmt.Errorf("kid erzeugen: %w", err)
	}
	secret, err := randToken("", secretRandBytes)
	if err != nil {
		return Key{}, "", fmt.Errorf("secret erzeugen: %w", err)
	}
	k := Key{
		KID:        kid,
		Name:       name,
		SecretHash: HashSecret(secret),
		Scopes:     scopes,
		Status:     StatusActive,
		CreatedAt:  time.Now().UTC(),
	}
	return k, secret, nil
}

// randToken liefert prefix + Base32-Kodierung von n Zufallsbytes.
func randToken(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + idEncoding.EncodeToString(b), nil
}
