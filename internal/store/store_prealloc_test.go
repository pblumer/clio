package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// TestInitialMmapSizePreallocatesFile prüft, dass eine gesetzte InitialMmapSize
// die Datei real auf (mindestens) diese Größe vorbelegt.
func TestInitialMmapSizePreallocatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prealloc.db")
	const want = 8 << 20 // 8 MiB

	st, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, InitialMmapSize: want})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() < want {
		t.Errorf("Dateigröße = %d, want >= %d", fi.Size(), want)
	}

	// Schreiben/Lesen funktioniert über der vorbelegten Datei unverändert.
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	got, err := st.Read("/a", false, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

// TestInitialMmapSizeNeverShrinks stellt sicher, dass das erneute Öffnen mit
// einer kleineren InitialMmapSize die bereits größere Datei NICHT verkleinert
// (das würde bbolt-Seiten abschneiden) und die Daten erhalten bleiben.
func TestInitialMmapSizeNeverShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noshrink.db")
	const big = 16 << 20 // 16 MiB

	st, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, InitialMmapSize: big})
	if err != nil {
		t.Fatalf("open groß: %v", err)
	}
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/keep", Type: "t"})
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat vorher: %v", err)
	}

	// Erneut öffnen mit deutlich kleinerer Zielgröße: die Datei darf nicht
	// schrumpfen.
	st2, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, InitialMmapSize: 1 << 20})
	if err != nil {
		t.Fatalf("open klein: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat nachher: %v", err)
	}
	if after.Size() < before.Size() {
		t.Errorf("Datei geschrumpft: %d -> %d", before.Size(), after.Size())
	}

	got, err := st2.Read("/keep", false, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Daten verloren: len = %d, want 1", len(got))
	}
}

// TestEnsureFileSizeGrowOnly testet den Helfer direkt: wächst auf die Zielgröße,
// verkleinert aber nie.
func TestEnsureFileSizeGrowOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, make([]byte, 1024), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ensureFileSize(path, 4096); err != nil {
		t.Fatalf("grow: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Size() != 4096 {
		t.Errorf("nach grow: %d, want 4096", fi.Size())
	}

	// Kleinere Zielgröße darf nicht verkleinern.
	if err := ensureFileSize(path, 2048); err != nil {
		t.Fatalf("noop: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Size() != 4096 {
		t.Errorf("unerwartet verkleinert: %d, want 4096", fi.Size())
	}
}
