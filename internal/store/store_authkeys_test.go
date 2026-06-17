package store

import (
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

func TestAuthKeysRoundtrip(t *testing.T) {
	st := openTemp(t)

	// Frischer Bund ist leer.
	if n, err := st.CountKeys(); err != nil || n != 0 {
		t.Fatalf("CountKeys auf leerem bund = %d, %v; want 0", n, err)
	}
	if _, found, err := st.GetKey("kid_unbekannt"); err != nil || found {
		t.Fatalf("GetKey(unbekannt): found=%v err=%v; want found=false", found, err)
	}

	k1, _, err := auth.GenerateKey("ci-writer", []auth.Scope{auth.ScopeRead, auth.ScopeWrite})
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	k2, _, err := auth.GenerateKey("admin", []auth.Scope{auth.ScopeAdmin})
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if err := st.PutKey(k1); err != nil {
		t.Fatalf("PutKey k1: %v", err)
	}
	if err := st.PutKey(k2); err != nil {
		t.Fatalf("PutKey k2: %v", err)
	}

	if n, _ := st.CountKeys(); n != 2 {
		t.Fatalf("CountKeys = %d, want 2", n)
	}

	got, found, err := st.GetKey(k1.KID)
	if err != nil || !found {
		t.Fatalf("GetKey(k1): found=%v err=%v", found, err)
	}
	if got.Name != "ci-writer" || got.SecretHash != k1.SecretHash || got.Status != auth.StatusActive {
		t.Fatalf("GetKey(k1) = %+v, unerwartet", got)
	}
	if !got.HasScope(auth.ScopeWrite) || got.HasScope(auth.ScopeAdmin) {
		t.Fatalf("scopes nach roundtrip falsch: %v", got.Scopes)
	}

	keys, err := st.ListKeys()
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("ListKeys = %d keys, want 2", len(keys))
	}
}

func TestRevokeKey(t *testing.T) {
	st := openTemp(t)

	// Unbekannter kid -> false, kein Fehler.
	if ok, err := st.RevokeKey("kid_unbekannt"); err != nil || ok {
		t.Fatalf("RevokeKey(unbekannt) = %v, %v; want false, nil", ok, err)
	}

	k, _, _ := auth.GenerateKey("temp", []auth.Scope{auth.ScopeRead})
	if err := st.PutKey(k); err != nil {
		t.Fatalf("PutKey: %v", err)
	}

	ok, err := st.RevokeKey(k.KID)
	if err != nil || !ok {
		t.Fatalf("RevokeKey = %v, %v; want true, nil", ok, err)
	}

	got, _, _ := st.GetKey(k.KID)
	if got.Status != auth.StatusRevoked {
		t.Fatalf("status nach widerruf = %q, want revoked", got.Status)
	}
	if got.RevokedAt == nil {
		t.Fatal("revokedAt ist nach widerruf nil")
	}
	firstRevokedAt := *got.RevokedAt

	// Widerruf ist idempotent: erneut widerrufen ändert revokedAt nicht.
	ok, err = st.RevokeKey(k.KID)
	if err != nil || !ok {
		t.Fatalf("zweiter RevokeKey = %v, %v; want true, nil", ok, err)
	}
	got, _, _ = st.GetKey(k.KID)
	if got.RevokedAt == nil || !got.RevokedAt.Equal(firstRevokedAt) {
		t.Fatal("revokedAt wurde beim zweiten widerruf überschrieben")
	}
}

// TestResetLeavesAuthKeys ist der Regressionstest zu ADR-022/ADR-025: der
// Dev-Reset darf den Schlüsselbund nicht anrühren, sonst sperrt man sich aus.
func TestResetLeavesAuthKeys(t *testing.T) {
	st := openTemp(t)

	k, _, _ := auth.GenerateKey("survivor", []auth.Scope{auth.ScopeAdmin})
	if err := st.PutKey(k); err != nil {
		t.Fatalf("PutKey: %v", err)
	}

	if _, err := st.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	got, found, err := st.GetKey(k.KID)
	if err != nil {
		t.Fatalf("GetKey nach Reset: %v", err)
	}
	if !found {
		t.Fatal("schlüssel nach Reset verschwunden — auth_keys wurde fälschlich geleert")
	}
	if got.SecretHash != k.SecretHash {
		t.Fatal("schlüssel nach Reset verändert")
	}
	if n, _ := st.CountKeys(); n != 1 {
		t.Fatalf("CountKeys nach Reset = %d, want 1", n)
	}
}
