package store

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

func TestRotateKey(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "keys.db"), Options{})

	k, oldSecret, err := auth.GenerateKey("admin", []auth.Scope{auth.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutKey(k); err != nil {
		t.Fatal(err)
	}
	oldHash := k.SecretHash

	wire, found, err := s.RotateKey(k.KID)
	if err != nil || !found {
		t.Fatalf("rotate: found=%v err=%v", found, err)
	}
	// Der zurückgegebene Leitungswert beginnt mit kid.
	if wire[:len(k.KID)+1] != k.KID+"." {
		t.Fatalf("wire %q beginnt nicht mit %q.", wire, k.KID)
	}

	got, ok, err := s.GetKey(k.KID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	// Hash hat sich geändert; das alte Geheimnis passt nicht mehr.
	if got.SecretHash == oldHash {
		t.Fatal("SecretHash unverändert nach rotate")
	}
	if got.SecretHash == auth.HashSecret(oldSecret) {
		t.Fatal("altes secret passt noch nach rotate")
	}
	// Scopes/Status bleiben erhalten.
	if !got.HasScope(auth.ScopeAdmin) || !got.Active() {
		t.Fatalf("scopes/status nach rotate verändert: %+v", got)
	}
	// Der neue Leitungswert passt zum gespeicherten Hash.
	newSecret := wire[len(k.KID)+1:]
	if got.SecretHash != auth.HashSecret(newSecret) {
		t.Fatal("neuer wire-wert passt nicht zum gespeicherten hash")
	}
}

func TestRotateKeyUnknown(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "keys.db"), Options{})
	_, found, err := s.RotateKey("kid_doesnotexist")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("rotate unbekannter kid: found=true")
	}
}

func TestPutGetKeyMetadataRoundTrip(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "keys.db"), Options{})
	k, _, err := auth.GenerateKey("legacy", []auth.Scope{auth.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	// Ein Key OHNE Metadaten lädt unverändert (Backward-Compat).
	if err := s.PutKey(k); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetKey(k.KID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ExpiresAt != nil || got.Owner != "" {
		t.Fatalf("unerwartete metadaten: %+v", got)
	}
}
