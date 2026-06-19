package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/store"
)

// extractWire holt den `kid.secret`-Wert aus der "secret (nur jetzt sichtbar):"-Zeile.
func extractWire(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, ": "); i >= 0 && strings.Contains(line, "secret") {
			return strings.TrimSpace(line[i+2:])
		}
	}
	t.Fatalf("kein secret in ausgabe: %s", out)
	return ""
}

func TestKeysCreateListRotateRevoke(t *testing.T) {
	db := filepath.Join(t.TempDir(), "clio.db")

	// create
	var out bytes.Buffer
	if err := runKeys([]string{"create", "--db", db, "--name", "ops", "--scopes", "read,admin", "--owner", "team", "--expires", "720h"}, &out); err != nil {
		t.Fatalf("create: %v\n%s", err, out.String())
	}
	wire := extractWire(t, out.String())
	kid := wire[:strings.IndexByte(wire, '.')]

	// Das Geheimnis muss zum gespeicherten Hash passen.
	assertKeyHashMatches(t, db, kid, wire)

	// list (ohne Secret)
	out.Reset()
	if err := runKeys([]string{"list", "--db", db}, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), kid) || strings.Contains(out.String(), "secretHash") {
		t.Fatalf("list-ausgabe unerwartet: %s", out.String())
	}

	// rotate — neuer Wert, passt zum neuen Hash
	out.Reset()
	if err := runKeys([]string{"rotate", "--db", db, "--kid", kid}, &out); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	newWire := extractWire(t, out.String())
	if newWire == wire {
		t.Fatal("rotate lieferte denselben wert")
	}
	assertKeyHashMatches(t, db, kid, newWire)

	// revoke
	out.Reset()
	if err := runKeys([]string{"revoke", "--db", db, "--kid", kid}, &out); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	st := openStore(t, db)
	got, ok, err := st.GetKey(kid)
	_ = st.Close()
	if err != nil || !ok || got.Status != auth.StatusRevoked {
		t.Fatalf("nach revoke: ok=%v status=%v err=%v", ok, got.Status, err)
	}
}

func TestKeysCreateValidation(t *testing.T) {
	db := filepath.Join(t.TempDir(), "clio.db")
	var out bytes.Buffer
	if err := runKeys([]string{"create", "--db", db, "--scopes", "read"}, &out); err == nil {
		t.Fatal("create ohne --name: kein fehler")
	}
	out.Reset()
	if err := runKeys([]string{"create", "--db", db, "--name", "x", "--scopes", "bogus"}, &out); err == nil {
		t.Fatal("create mit ungültigem scope: kein fehler")
	}
	out.Reset()
	if err := runKeys([]string{"rotate", "--db", db, "--kid", "kid_unknown"}, &out); err == nil {
		t.Fatal("rotate unbekannter kid: kein fehler")
	}
}

// TestKeysCLIWritesAudit: Offline-Key-Aktionen schreiben Audit-Einträge (Actor
// "cli", ADR-031).
func TestKeysCLIWritesAudit(t *testing.T) {
	db := filepath.Join(t.TempDir(), "clio.db")
	var out bytes.Buffer
	if err := runKeys([]string{"create", "--db", db, "--name", "ops", "--scopes", "admin"}, &out); err != nil {
		t.Fatalf("create: %v", err)
	}
	st := openStore(t, db)
	defer func() { _ = st.Close() }()
	entries, err := st.AuditEntries(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "key.create" || entries[0].ActorName != "cli" {
		t.Fatalf("audit-eintrag unerwartet: %+v", entries)
	}
}

func TestParseExpiry(t *testing.T) {
	if _, err := parseExpiry("720h"); err != nil {
		t.Fatalf("dauer: %v", err)
	}
	if _, err := parseExpiry("2026-12-31T00:00:00Z"); err != nil {
		t.Fatalf("rfc3339: %v", err)
	}
	if _, err := parseExpiry("garbage"); err == nil {
		t.Fatal("garbage sollte fehler liefern")
	}
}

func openStore(t *testing.T, db string) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(db, store.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st
}

func assertKeyHashMatches(t *testing.T, db, kid, wire string) {
	t.Helper()
	secret := wire[strings.IndexByte(wire, '.')+1:]
	st := openStore(t, db)
	defer func() { _ = st.Close() }()
	k, ok, err := st.GetKey(kid)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", kid, ok, err)
	}
	if k.SecretHash != auth.HashSecret(secret) {
		t.Fatalf("hash passt nicht zum ausgegebenen secret für %s", kid)
	}
}
