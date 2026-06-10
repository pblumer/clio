package store

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func benchStore(b *testing.B, mode SyncMode) *Store {
	b.Helper()
	st, err := OpenWithOptions(filepath.Join(b.TempDir(), "bench.db"), Options{SyncMode: mode})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleCandidate() event.Candidate {
	return event.Candidate{
		Source:  "https://bench.example",
		Subject: "/bench/stream",
		Type:    "benchmarked",
		Data:    []byte(`{"k":"v","n":42}`),
	}
}

// BenchmarkAppendSequential misst Einzelschreiber-Durchsatz je SyncMode.
// Hier ist Group Commit nicht im Vorteil (keine Nebenläufigkeit zum Bündeln)
// und kostet sogar die Batch-Verzögerung — der Kontrast zu Parallel zeigt,
// wofür Group Commit gedacht ist.
func BenchmarkAppendSequential(b *testing.B) {
	for _, m := range []struct {
		name string
		mode SyncMode
	}{{"group", SyncGroup}, {"always", SyncAlways}, {"off", SyncOff}} {
		b.Run(m.name, func(b *testing.B) {
			st := benchStore(b, m.mode)
			cand := []event.Candidate{sampleCandidate()}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := st.Append(cand, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAppendParallel misst den Durchsatz unter gleichzeitigen Schreibern.
// Hier spielt Group Commit seine Stärke aus: viele Writes teilen sich ein fsync.
func BenchmarkAppendParallel(b *testing.B) {
	for _, m := range []struct {
		name string
		mode SyncMode
	}{{"group", SyncGroup}, {"always", SyncAlways}, {"off", SyncOff}} {
		b.Run(m.name, func(b *testing.B) {
			st := benchStore(b, m.mode)
			cand := []event.Candidate{sampleCandidate()}
			// Viele gleichzeitige Clients simulieren (≫ Kerne), wie bei einem
			// ausgelasteten Server — erst so bündelt Group Commit wirksam.
			b.SetParallelism(64)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := st.Append(cand, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkReadRecursive misst rekursives Lesen über viele Subjects.
func BenchmarkReadRecursive(b *testing.B) {
	st := benchStore(b, SyncOff) // schnelles Befüllen
	for i := 0; i < 10000; i++ {
		if _, err := st.Append([]event.Candidate{sampleCandidate()}, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Read("/bench", true, ReadOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
