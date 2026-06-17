package main

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

func openTempStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "boot.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestBootstrapFromAdminKey: frischer Store + CLIO_BOOTSTRAP_ADMIN_KEY ergibt
// genau einen Admin-Key; ein zweiter Lauf ("Neustart") legt keinen weiteren an.
func TestBootstrapFromAdminKey(t *testing.T) {
	st := openTempStore(t)
	cfg := config.Config{BootstrapAdminKey: "super-geheim"}

	if err := bootstrapAuth(st, cfg, quietLogger()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	keys, _ := st.ListKeys()
	if len(keys) != 1 {
		t.Fatalf("anzahl keys = %d, want 1", len(keys))
	}
	k := keys[0]
	if k.Name != "bootstrap-admin" || !k.HasScope(auth.ScopeAdmin) || k.Status != auth.StatusActive {
		t.Fatalf("unerwarteter key: %+v", k)
	}
	if k.SecretHash != auth.HashSecret("super-geheim") {
		t.Fatal("secret-hash passt nicht zum bootstrap-wert")
	}

	// Kein erneutes Bootstrapping bei "Neustart" (Bund nicht mehr leer).
	if err := bootstrapAuth(st, cfg, quietLogger()); err != nil {
		t.Fatalf("zweiter bootstrap: %v", err)
	}
	if keys, _ := st.ListKeys(); len(keys) != 1 {
		t.Fatalf("nach zweitem bootstrap = %d keys, want 1 (kein re-bootstrap)", len(keys))
	}
}

// TestBootstrapFromLegacyToken: CLIO_API_TOKEN bootet einen deprecated
// legacy-token-Admin-Key.
func TestBootstrapFromLegacyToken(t *testing.T) {
	st := openTempStore(t)

	if err := bootstrapAuth(st, config.Config{APIToken: "altes-token"}, quietLogger()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	keys, _ := st.ListKeys()
	if len(keys) != 1 || keys[0].Name != "legacy-token" {
		t.Fatalf("erwartete genau einen legacy-token-key, bekam %+v", keys)
	}
	if keys[0].SecretHash != auth.HashSecret("altes-token") {
		t.Fatal("legacy secret-hash passt nicht")
	}
	if !keys[0].HasScope(auth.ScopeAdmin) {
		t.Fatal("legacy-key sollte admin-scope haben")
	}
}

// TestBootstrapPrefersAdminKey: ist beides gesetzt, hat CLIO_BOOTSTRAP_ADMIN_KEY
// Vorrang vor dem Legacy-Token.
func TestBootstrapPrefersAdminKey(t *testing.T) {
	st := openTempStore(t)
	cfg := config.Config{BootstrapAdminKey: "neu", APIToken: "alt"}

	if err := bootstrapAuth(st, cfg, quietLogger()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	keys, _ := st.ListKeys()
	if len(keys) != 1 || keys[0].Name != "bootstrap-admin" {
		t.Fatalf("bootstrap-admin sollte vorrang haben, bekam %+v", keys)
	}
}

// TestBootstrapNoAuthMaterial: leerer Bund ohne ENV-Material -> klarer Fehler.
func TestBootstrapNoAuthMaterial(t *testing.T) {
	st := openTempStore(t)
	if err := bootstrapAuth(st, config.Config{}, quietLogger()); err == nil {
		t.Fatal("erwartete fehler bei fehlendem auth-material, bekam nil")
	}
}

// TestBootstrapSkippedWhenKeysExist: bei nicht-leerem Bund wird nichts angelegt,
// selbst wenn Bootstrap-ENV gesetzt ist (höchstens ein Key, nur bei leerem Bund).
func TestBootstrapSkippedWhenKeysExist(t *testing.T) {
	st := openTempStore(t)
	existing, _, _ := auth.GenerateKey("vorhanden", []auth.Scope{auth.ScopeRead})
	if err := st.PutKey(existing); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := bootstrapAuth(st, config.Config{BootstrapAdminKey: "x"}, quietLogger()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	keys, _ := st.ListKeys()
	if len(keys) != 1 || keys[0].Name != "vorhanden" {
		t.Fatalf("vorhandener bund wurde verändert: %+v", keys)
	}
}
