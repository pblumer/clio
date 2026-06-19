package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// makeDB legt eine kleine DB mit n Events an und schließt sie wieder.
func makeDB(t *testing.T, path string, n int) {
	t.Helper()
	s, err := store.OpenWithOptions(path, store.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cands := make([]event.Candidate, n)
	for i := range cands {
		cands[i] = event.Candidate{Source: "t", Subject: "/x", Type: "t.evt", Data: []byte(`{"i":1}`)}
	}
	if _, err := s.Append(cands, nil); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunBackupRestoreVerifyCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "clio.db")
	snap := filepath.Join(dir, "snap.clio")
	target := filepath.Join(dir, "restored.db")
	makeDB(t, db, 7)

	var out bytes.Buffer
	if err := runBackup([]string{"--db", db, "--output", snap, "--verify"}, &out); err != nil {
		t.Fatalf("runBackup: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "verifiziert") {
		t.Fatalf("backup-ausgabe ohne verify-marker: %s", out.String())
	}

	out.Reset()
	if err := runRestore([]string{"--input", snap, "--db", target}, &out); err != nil {
		t.Fatalf("runRestore: %v", err)
	}

	out.Reset()
	if err := runVerify([]string{"--db", target, "--json"}, &out); err != nil {
		t.Fatalf("runVerify: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"ok": true`) {
		t.Fatalf("verify-json nicht ok: %s", out.String())
	}
}

func TestRunBackupRequiresOutput(t *testing.T) {
	var out bytes.Buffer
	if err := runBackup([]string{"--db", "x.db"}, &out); err == nil {
		t.Fatal("runBackup ohne --output: kein fehler")
	}
}

func TestRunRestoreRequiresInput(t *testing.T) {
	var out bytes.Buffer
	if err := runRestore([]string{"--db", "x.db"}, &out); err == nil {
		t.Fatal("runRestore ohne --input: kein fehler")
	}
}

// TestRunVerifyBrokenChain: eine manipulierte DB führt zu Exit-Fehler.
func TestRunVerifyBrokenChain(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "clio.db")
	makeDB(t, db, 3)

	// Roh die Datei verfälschen: irgendeinen Event-JSON-Wert kippen. Wir suchen
	// nach dem Typ-String und ersetzen ihn — das bricht Hash/Kette.
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte("t.evt"), []byte("x.evt"), 1)
	if bytes.Equal(raw, tampered) {
		t.Skip("kein ersetzbarer marker gefunden")
	}
	if err := os.WriteFile(db, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = runVerify([]string{"--db", db}, &out)
	if err == nil {
		t.Fatalf("runVerify auf manipulierter DB: kein fehler\n%s", out.String())
	}
}
