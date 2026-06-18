package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// TestCompactInPlacePreservesData prüft die Online-Kompaktierung: Daten und
// Hash-Kette bleiben erhalten, und der Store ist danach für Lesen UND Schreiben
// weiter nutzbar (der db-Pointer wurde sauber getauscht).
func TestCompactInPlacePreservesData(t *testing.T) {
	st := openTemp(t)
	for i := 0; i < 200; i++ {
		appendAll(t, st, event.Candidate{
			Source: "s", Subject: "/s", Type: "t",
			Data: json.RawMessage(`{"n":` + itoa(i) + `}`),
		})
	}

	old, neu, err := st.CompactInPlace()
	if err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	if old <= 0 || neu <= 0 {
		t.Fatalf("größen unplausibel: old=%d new=%d", old, neu)
	}

	// Daten/Kette unverändert ...
	if c, _ := st.Count(); c != 200 {
		t.Fatalf("count nach compact = %d, want 200", c)
	}
	if res, _ := st.Verify(); !res.OK || res.Count != 200 {
		t.Fatalf("verify nach compact: %+v", res)
	}
	// ... und der Store nimmt weitere Writes an (Reopen hat funktioniert).
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/s", Type: "t"})
	if c, _ := st.Count(); c != 201 {
		t.Fatalf("count nach weiterem write = %d, want 201", c)
	}
}

// TestLastCompactionTracking prüft, dass CompactInPlace den letzten Online-Compact
// festhält (für die Dashboard-Anzeige) und vorher nichts gemeldet wird.
func TestLastCompactionTracking(t *testing.T) {
	st := openTemp(t)
	if _, ok := st.LastCompaction(); ok {
		t.Fatal("vor jedem Compact sollte LastCompaction ok=false liefern")
	}
	for i := 0; i < 50; i++ {
		appendAll(t, st, event.Candidate{Source: "s", Subject: "/s", Type: "t"})
	}
	if _, _, err := st.CompactInPlace(); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	lc, ok := st.LastCompaction()
	if !ok {
		t.Fatal("nach CompactInPlace sollte LastCompaction ok=true liefern")
	}
	if lc.At.IsZero() || lc.OldBytes <= 0 || lc.NewBytes <= 0 {
		t.Fatalf("CompactionInfo unplausibel: %+v", lc)
	}
}

// TestCompactInPlacePreservesPreallocation stellt sicher, dass der Reopen nach
// der Kompaktierung die Vorbelegung wiederherstellt — sonst schrumpfte die Datei
// auf Datengröße und die Remap-Stalls kehrten zurück.
func TestCompactInPlacePreservesPreallocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prealloc-compact.db")
	const initial = 16 << 20
	st, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, InitialMmapSize: initial})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})

	if _, _, err := st.CompactInPlace(); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() < initial {
		t.Errorf("Datei nach Compact = %d, want >= %d (Vorbelegung wiederhergestellt)", fi.Size(), initial)
	}
	if c, _ := st.Count(); c != 1 {
		t.Fatalf("count nach compact = %d, want 1", c)
	}
}

// TestCompactInPlaceConcurrent ist der Kern-Sicherheitstest (mit -race fahren):
// während mehrere Goroutinen schreiben und lesen, wird mehrfach online
// kompaktiert. Erwartung: keine Races/Panics, alle Writes landen, Kette gültig.
func TestCompactInPlaceConcurrent(t *testing.T) {
	st := openTemp(t)

	const writers = 4
	const perWriter = 150

	var workers sync.WaitGroup // Schreiber + Kompaktierer (endlich)
	var readers sync.WaitGroup // Leser (laufen bis stop)
	var writeErr, readErr atomic.Int64
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		workers.Add(1)
		go func(w int) {
			defer workers.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := st.Append([]event.Candidate{{
					Source: "s", Subject: "/c", Type: "t",
					Data: json.RawMessage(`{"w":` + itoa(w) + `}`),
				}}, nil); err != nil {
					writeErr.Add(1)
				}
			}
		}(w)
	}

	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := st.Count(); err != nil {
						readErr.Add(1)
					}
					if _, err := st.Stats(); err != nil {
						readErr.Add(1)
					}
				}
			}
		}()
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 5; i++ {
			if _, _, err := st.CompactInPlace(); err != nil {
				t.Errorf("CompactInPlace: %v", err)
			}
		}
	}()

	workers.Wait() // Schreiber + Kompaktierer fertig
	close(stop)    // Leser beenden
	readers.Wait()

	if n := writeErr.Load(); n != 0 {
		t.Errorf("%d Schreibfehler unter Last/Compaction", n)
	}
	if n := readErr.Load(); n != 0 {
		t.Errorf("%d Lesefehler unter Last/Compaction", n)
	}
	if c, _ := st.Count(); c != writers*perWriter {
		t.Fatalf("count = %d, want %d", c, writers*perWriter)
	}
	if res, _ := st.Verify(); !res.OK {
		t.Fatalf("Hash-Kette nach paralleler Compaction ungültig: %+v", res)
	}
}
