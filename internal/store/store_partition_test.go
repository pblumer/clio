package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// fixedClock setzt eine deterministische Uhr (für reproduzierbare Hashes).
func fixedClock(st *Store) {
	t := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return t }
}

func openParts(t *testing.T, n int) *Store {
	t.Helper()
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "clio.db"), Options{SyncMode: SyncGroup, Partitions: n})
	if err != nil {
		t.Fatalf("open (n=%d): %v", n, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func cand(source, subject, typ string) event.Candidate {
	return event.Candidate{Source: source, Subject: subject, Type: typ, Data: []byte(`{"k":1}`)}
}

func shardCount(t *testing.T, sh *shard) uint64 {
	t.Helper()
	var n uint64
	if err := sh.view(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketEvents).Sequence()
		return nil
	}); err != nil {
		t.Fatalf("shardCount: %v", err)
	}
	return n
}

// TestPartitionSinglePartitionIdenticalToDefault belegt: Options{Partitions:1} ist
// hash-/ID-identisch zum Default (kein Partitions-Feld). Das ist die n=1-Invariante
// (ARCHITECTURE.md §4.1, WP-2 Akzeptanz (a)).
func TestPartitionSinglePartitionIdenticalToDefault(t *testing.T) {
	mk := func(opts Options) []event.Event {
		st, err := OpenWithOptions(filepath.Join(t.TempDir(), "c.db"), opts)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = st.Close() }()
		fixedClock(st)
		evs, err := st.Append([]event.Candidate{
			cand("/svc/a", "/orders/1", "created"),
			cand("/svc/a", "/orders/1", "updated"),
			cand("/svc/a", "/orders/2", "created"),
		}, nil)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		return evs
	}
	def := mk(Options{SyncMode: SyncGroup})
	one := mk(Options{SyncMode: SyncGroup, Partitions: 1})
	if len(def) != len(one) {
		t.Fatalf("Länge unterschiedlich: %d vs %d", len(def), len(one))
	}
	for i := range def {
		if def[i].ID != one[i].ID || def[i].Hash != one[i].Hash || def[i].PredecessorHash != one[i].PredecessorHash {
			t.Errorf("Event %d weicht ab:\n default=%+v\n n=1   =%+v", i, def[i], one[i])
		}
	}
}

// TestPartitionRoutingAndPerPartitionChains: bei N>1 landen Events nach `source`
// deterministisch in der richtigen Partition, jede Partition führt eine lückenlose
// Kette (Verify grün), Count summiert korrekt (WP-2 Akzeptanz (b)).
func TestPartitionRoutingAndPerPartitionChains(t *testing.T) {
	const n = 8
	st := openParts(t, n)
	fixedClock(st)

	want := make([]uint64, n)
	const total = 300
	for i := 0; i < total; i++ {
		src := fmt.Sprintf("/svc/%d", i)
		pid := st.ring.PartitionForSource(src)
		want[pid]++
		if _, err := st.Append([]event.Candidate{cand(src, fmt.Sprintf("/o/%d", i), "t")}, nil); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	nonEmpty := 0
	for id, sh := range st.shards {
		got := shardCount(t, sh)
		if got != want[id] {
			t.Errorf("Partition %d: %d Events, erwartet %d", id, got, want[id])
		}
		if got > 0 {
			nonEmpty++
		}
	}
	if nonEmpty < 2 {
		t.Errorf("erwartet Verteilung über mehrere Partitionen, nur %d belegt", nonEmpty)
	}

	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK {
		t.Errorf("verify nicht OK: %+v", res)
	}
	if res.Count != total {
		t.Errorf("verify Count = %d, want %d", res.Count, total)
	}
	if c, err := st.Count(); err != nil || c != total {
		t.Errorf("Count = %d (err %v), want %d", c, err, total)
	}
}

// twoDistinctPartitionSources liefert zwei Sources, die in verschiedene Partitionen
// fallen (für Mixed-Batch- und Fan-out-Tests).
func twoDistinctPartitionSources(st *Store) (string, string, bool) {
	a := "/svc/a-0"
	pa := st.ring.PartitionForSource(a)
	for i := 0; i < 10000; i++ {
		b := fmt.Sprintf("/svc/b-%d", i)
		if st.ring.PartitionForSource(b) != pa {
			return a, b, true
		}
	}
	return "", "", false
}

// TestPartitionMixedBatchRejected: ein Append-Aufruf mit Sources aus verschiedenen
// Partitionen wird mit ErrMixedPartition abgelehnt (WP-2: Mixed-Batch → 400).
func TestPartitionMixedBatchRejected(t *testing.T) {
	st := openParts(t, 8)
	fixedClock(st)
	a, b, ok := twoDistinctPartitionSources(st)
	if !ok {
		t.Skip("keine zwei Sources in verschiedenen Partitionen gefunden")
	}
	_, err := st.Append([]event.Candidate{cand(a, "/s", "t"), cand(b, "/s", "t")}, nil)
	if !errors.Is(err, ErrMixedPartition) {
		t.Fatalf("erwartet ErrMixedPartition, bekam %v", err)
	}
	// Nichts darf geschrieben worden sein.
	if c, _ := st.Count(); c != 0 {
		t.Errorf("nach abgelehntem Mixed-Batch: Count = %d, want 0", c)
	}
}

// TestPartitionReadFanOut: ein Subject, das von Sources in verschiedenen Partitionen
// beschrieben wurde, wird vollständig (partitionsübergreifend) zurückgelesen.
func TestPartitionReadFanOut(t *testing.T) {
	st := openParts(t, 8)
	fixedClock(st)
	a, b, ok := twoDistinctPartitionSources(st)
	if !ok {
		t.Skip("keine zwei Sources in verschiedenen Partitionen gefunden")
	}
	const subject = "/shared/stream"
	if _, err := st.Append([]event.Candidate{cand(a, subject, "t")}, nil); err != nil {
		t.Fatalf("append a: %v", err)
	}
	if _, err := st.Append([]event.Candidate{cand(b, subject, "t")}, nil); err != nil {
		t.Fatalf("append b: %v", err)
	}
	evs, err := st.ReadSubject(subject, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(evs) != 2 {
		t.Errorf("Fan-out-Read lieferte %d Events, erwartet 2 (über zwei Partitionen)", len(evs))
	}
	if n, err := st.CountSubject(subject, false); err != nil || n != 2 {
		t.Errorf("CountSubject = %d (err %v), want 2", n, err)
	}
}

// TestPartitionParallelWritesRace: gleichzeitige Schreibvorgänge in verschiedene
// Partitionen laufen ohne gemeinsamen Datei-Lock; unter `-race` deckt das Datenraces
// auf (WP-2 Akzeptanz (c)/(d)). Verify bleibt grün, Count stimmt.
func TestPartitionParallelWritesRace(t *testing.T) {
	const (
		n        = 8
		writers  = 8
		perWrite = 50
	)
	st := openParts(t, n)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWrite; j++ {
				src := fmt.Sprintf("/w/%d/%d", w, j)
				if _, err := st.Append([]event.Candidate{cand(src, "/s", "t")}, nil); err != nil {
					t.Errorf("parallel append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	res, err := st.Verify()
	if err != nil || !res.OK {
		t.Errorf("verify nach parallelen Writes: %+v (err %v)", res, err)
	}
	want := uint64(writers * perWrite)
	if c, err := st.Count(); err != nil || c != want {
		t.Errorf("Count = %d (err %v), want %d", c, err, want)
	}
}
