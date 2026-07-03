package auth

import (
	"strings"
	"testing"
)

// TestVerifySecretLegacyAndKDF prüft den formatunabhängigen, zeitkonstanten
// Secret-Vergleich (WP-4.4/S-H2): sowohl der alte, schnelle SHA-256-Hex-Hash
// (HashSecret) als auch der gesalzene KDF (HashSecretKDF) werden korrekt verifiziert;
// falsche Secrets werden in beiden Formaten abgelehnt.
func TestVerifySecretLegacyAndKDF(t *testing.T) {
	const secret = "super-geheim"

	legacy := HashSecret(secret)
	if !VerifySecret(legacy, secret) {
		t.Fatal("legacy: korrektes secret abgelehnt")
	}
	if VerifySecret(legacy, "falsch") {
		t.Fatal("legacy: falsches secret akzeptiert")
	}

	kdf, err := HashSecretKDF(secret)
	if err != nil {
		t.Fatalf("HashSecretKDF: %v", err)
	}
	if !strings.HasPrefix(kdf, kdfScheme+"$") {
		t.Fatalf("KDF-Hash hat unerwartetes Format: %q", kdf)
	}
	if !VerifySecret(kdf, secret) {
		t.Fatal("kdf: korrektes secret abgelehnt")
	}
	if VerifySecret(kdf, "falsch") {
		t.Fatal("kdf: falsches secret akzeptiert")
	}
}

// TestHashSecretKDFSalted stellt sicher, dass zwei KDF-Hashes desselben Geheimnisses
// dank frischem Salt verschieden sind (kein Rainbow-Table-/Korrelationsangriff), aber
// beide gegen das Geheimnis verifizieren.
func TestHashSecretKDFSalted(t *testing.T) {
	h1, err := HashSecretKDF("gleich")
	if err != nil {
		t.Fatalf("HashSecretKDF: %v", err)
	}
	h2, err := HashSecretKDF("gleich")
	if err != nil {
		t.Fatalf("HashSecretKDF: %v", err)
	}
	if h1 == h2 {
		t.Fatal("zwei KDF-Hashes desselben Secrets sind identisch — Salt greift nicht")
	}
	if !VerifySecret(h1, "gleich") || !VerifySecret(h2, "gleich") {
		t.Fatal("gesalzene Hashes verifizieren nicht gegen das Secret")
	}
}

// TestVerifySecretRejectsMalformedKDF prüft, dass kaputte KDF-Strings sicher als
// „kein Treffer" gelten statt zu panicken.
func TestVerifySecretRejectsMalformedKDF(t *testing.T) {
	for _, bad := range []string{
		kdfScheme + "$",
		kdfScheme + "$abc$salt$dk",     // nicht-numerische Iteration
		kdfScheme + "$1000$!!!$dk",     // ungültiges Salt-base64
		kdfScheme + "$1000$c2FsdA$!!!", // ungültiges dk-base64
		kdfScheme + "$1000$c2FsdA",     // zu wenige Felder
	} {
		if VerifySecret(bad, "egal") {
			t.Fatalf("kaputter KDF-String fälschlich akzeptiert: %q", bad)
		}
	}
}
