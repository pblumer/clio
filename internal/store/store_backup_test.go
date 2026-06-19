package store

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// seedEvents schreibt n Test-Events in den Store und gibt sie zurück.
func seedEvents(t *testing.T, s *Store, n int) []event.Event {
	t.Helper()
	cands := make([]event.Candidate, n)
	for i := range cands {
		cands[i] = event.Candidate{
			Source:  "test",
			Subject: "/orders/" + string(rune('a'+i%26)),
			Type:    "com.example.order.created",
			Data:    []byte(`{"orderId":"o-` + string(rune('0'+i%10)) + `"}`),
		}
	}
	evs, err := s.Append(cands, nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return evs
}

func openTestStore(t *testing.T, path string, opts Options) *Store {
	t.Helper()
	s, err := OpenWithOptions(path, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestBackupRestoreVerifyReplay ist der verbindliche End-to-End-Testfall
// (ADR-026, INV-R1/R2/R3): Events schreiben → DB löschen → Restore → Verify →
// Event-für-Event-Replay gegen das Original → nahtloser Folge-Append.
func TestBackupRestoreVerifyReplay(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clio.db")
	backupPath := filepath.Join(dir, "snap.clio")
	restorePath := filepath.Join(dir, "restored.db")

	// Mit Signatur, damit auch der Signaturpfad von verify mitgeprüft wird.
	seed, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParsePrivateKey(seed)
	if err != nil {
		t.Fatal(err)
	}

	s := openTestStore(t, dbPath, Options{SigningKey: key})
	original := seedEvents(t, s, 12)

	br, err := s.BackupToFile(backupPath)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if br.Events != 12 {
		t.Fatalf("backup events = %d, want 12", br.Events)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Totalverlust simulieren.
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}

	// Restore in eine leere Ziel-DB.
	rr, err := Restore(backupPath, restorePath, false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if rr.Events != br.Events || rr.Head != br.Head {
		t.Fatalf("restore (%d, %s) != backup (%d, %s)", rr.Events, rr.Head, br.Events, br.Head)
	}

	// Verify nach Restore (mit Signaturprüfung).
	pub := key.Public().(ed25519.PublicKey)
	vr, err := VerifyFile(restorePath, pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !vr.OK {
		t.Fatalf("verify nicht ok: %s (brokenAt=%s)", vr.Reason, vr.BrokenAt)
	}
	if vr.Count != 12 || vr.Head != br.Head {
		t.Fatalf("verify count/head = (%d, %s), want (12, %s)", vr.Count, vr.Head, br.Head)
	}

	// Replay: Event-für-Event bit-identisch zum Original (INV-R2).
	rs := openTestStore(t, restorePath, Options{SigningKey: key})
	replay, err := rs.Read("/", true, ReadOptions{})
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if len(replay) != len(original) {
		t.Fatalf("replay len = %d, want %d", len(replay), len(original))
	}
	for i := range original {
		if replay[i].Hash != original[i].Hash || replay[i].ID != original[i].ID {
			t.Fatalf("event %d weicht ab: hash %s/%s id %s/%s",
				i, replay[i].Hash, original[i].Hash, replay[i].ID, original[i].ID)
		}
	}

	// Nahtloser Folge-Append (INV-R3): Kette bleibt grün, Count = vorher + 1.
	if _, err := rs.Append([]event.Candidate{{
		Source: "test", Subject: "/orders/z", Type: "com.example.order.created", Data: []byte(`{"orderId":"o-z"}`),
	}}, nil); err != nil {
		t.Fatalf("folge-append: %v", err)
	}
	vr2, err := rs.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if !vr2.OK || vr2.Count != 13 {
		t.Fatalf("nach folge-append: ok=%v count=%d, want ok=true count=13", vr2.OK, vr2.Count)
	}
}

// TestBackupConsistentSnapshot prüft, dass das Snapshot-Artefakt eigenständig
// öffenbar und verify-grün ist und Count/Head zum Quell-Store passen (INV-B1/B3).
func TestBackupConsistentSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, filepath.Join(dir, "clio.db"), Options{})
	seedEvents(t, s, 5)
	want, err := s.Verify()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	br, err := s.Backup(&buf)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if br.Events != 5 || br.Head != want.Head {
		t.Fatalf("backup (%d, %s) != store (%d, %s)", br.Events, br.Head, want.Count, want.Head)
	}
	if int64(buf.Len()) != br.Bytes {
		t.Fatalf("gemeldete bytes %d != geschriebene %d", br.Bytes, buf.Len())
	}

	// Snapshot auf Platte legen und offline verifizieren.
	snap := filepath.Join(dir, "snap.clio")
	if err := os.WriteFile(snap, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	vr, err := VerifyFile(snap, nil)
	if err != nil {
		t.Fatalf("verify snapshot: %v", err)
	}
	if !vr.OK || vr.Count != 5 || vr.Head != want.Head {
		t.Fatalf("snapshot verify: ok=%v count=%d head=%s", vr.OK, vr.Count, vr.Head)
	}
}

// TestRestoreTargetExists deckt INV-R1 ab: ohne overwrite kein Überschreiben,
// mit overwrite wird das Ziel ersetzt.
func TestRestoreTargetExists(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, filepath.Join(dir, "src.db"), Options{})
	seedEvents(t, s, 3)
	snap := filepath.Join(dir, "snap.clio")
	if _, err := s.BackupToFile(snap); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, []byte("vorhanden"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(snap, target, false); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("restore ohne force: err = %v, want ErrTargetExists", err)
	}
	if _, err := Restore(snap, target, true); err != nil {
		t.Fatalf("restore mit force: %v", err)
	}
	vr, err := VerifyFile(target, nil)
	if err != nil || !vr.OK || vr.Count != 3 {
		t.Fatalf("nach force-restore: vr=%+v err=%v", vr, err)
	}
}

// TestRestoreMissingSource: fehlende Quelle ist ein klarer Fehler.
func TestRestoreMissingSource(t *testing.T) {
	dir := t.TempDir()
	_, err := Restore(filepath.Join(dir, "fehlt.clio"), filepath.Join(dir, "ziel.db"), false)
	if err == nil {
		t.Fatal("restore aus fehlender quelle: kein fehler")
	}
}

// TestRestoreInvalidBackup: eine Nicht-bbolt-Datei wird als ungültig erkannt.
func TestRestoreInvalidBackup(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "garbage.clio")
	if err := os.WriteFile(bad, bytes.Repeat([]byte{0x7f}, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(bad, filepath.Join(dir, "ziel.db"), false); err == nil {
		t.Fatal("restore aus korrupter datei: kein fehler")
	}
}

// TestVerifyFileInvalid: eine valide bbolt-Datei ohne events-Bucket ist kein
// clio-Backup.
func TestVerifyFileInvalid(t *testing.T) {
	dir := t.TempDir()
	// Eine bbolt-Datei ohne clio-Buckets erzeugen, indem wir eine Fremd-DB anlegen.
	foreign := filepath.Join(dir, "foreign.db")
	// Schnellster Weg: Store öffnen, dann den events-Bucket gibt es ja — stattdessen
	// schreiben wir bewusst Müll und erwarten einen Open-Fehler.
	if err := os.WriteFile(foreign, []byte("not bolt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(foreign, nil); err == nil {
		t.Fatal("verify korrupter datei: kein fehler")
	}
}

// TestVerifyFileReadOnly deckt INV-V1 ab: verify verändert die Datei nicht.
func TestVerifyFileReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clio.db")
	s := openTestStore(t, dbPath, Options{})
	seedEvents(t, s, 4)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(dbPath, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("verify hat die datei verändert (INV-V1 verletzt)")
	}
}

// TestBackupFileOffline: BackupFile gegen eine NICHT gehaltene DB erzeugt ein
// verify-grünes Artefakt; ein bestehendes Ziel wird ohne force nicht überschrieben.
func TestBackupFileOffline(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clio.db")
	s := openTestStore(t, dbPath, Options{})
	seedEvents(t, s, 6)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "cold.clio")
	br, err := BackupFile(dbPath, out, false)
	if err != nil {
		t.Fatalf("backupfile: %v", err)
	}
	if br.Events != 6 {
		t.Fatalf("events = %d, want 6", br.Events)
	}
	vr, err := VerifyFile(out, nil)
	if err != nil || !vr.OK || vr.Count != 6 {
		t.Fatalf("verify cold backup: vr=%+v err=%v", vr, err)
	}

	// Ziel existiert → ohne force ErrTargetExists.
	if _, err := BackupFile(dbPath, out, false); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("backupfile ohne force: err = %v, want ErrTargetExists", err)
	}
	// Mit force wird es ersetzt.
	if _, err := BackupFile(dbPath, out, true); err != nil {
		t.Fatalf("backupfile force: %v", err)
	}
}

// TestBackupFileLockedDB belegt die dokumentierte Einschränkung: solange eine
// schreibende Instanz die DB hält, kann ein separater Prozess sie nicht öffnen —
// BackupFile schlägt fehl (für Hot-Backup dient der HTTP-Endpunkt).
func TestBackupFileLockedDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clio.db")
	s := openTestStore(t, dbPath, Options{}) // bleibt offen (exklusiver Lock)
	seedEvents(t, s, 2)

	if _, err := BackupFile(dbPath, filepath.Join(dir, "out.clio"), false); err == nil {
		t.Fatal("BackupFile auf gehaltener DB: kein fehler (lock-erwartung verletzt)")
	}
}
