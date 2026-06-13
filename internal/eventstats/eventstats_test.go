package eventstats

import (
	"testing"
	"time"
)

func TestAddAndSnapshot(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	h.Add(2, start)                              // Bucket 0
	h.Add(3, start.Add(500*time.Millisecond))    // ebenfalls Bucket 0 (1s breit)
	h.Add(1, start.Add(2*time.Second))           // Bucket 2

	s := h.Snapshot()
	if s.Width != time.Second {
		t.Fatalf("width = %v, want 1s", s.Width)
	}
	if s.Total != 6 {
		t.Fatalf("total = %d, want 6", s.Total)
	}
	if len(s.Counts) != 3 || s.Counts[0] != 5 || s.Counts[1] != 0 || s.Counts[2] != 1 {
		t.Fatalf("counts = %v, want [5 0 1]", s.Counts)
	}
}

func TestNonPositiveIsNoop(t *testing.T) {
	start := time.Now().UTC()
	h := New(start)
	h.Add(0, start)
	h.Add(-5, start)
	if s := h.Snapshot(); s.Total != 0 || len(s.Counts) != 0 {
		t.Fatalf("snapshot = %+v, want leer", s)
	}
}

func TestBeforeOriginClampsToBucketZero(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)
	h.Add(4, start.Add(-time.Hour)) // vor origin -> Bucket 0
	s := h.Snapshot()
	if s.Total != 4 || len(s.Counts) != 1 || s.Counts[0] != 4 {
		t.Fatalf("counts = %v total=%d, want [4] total=4", s.Counts, s.Total)
	}
}

func TestCompactionKeepsTotalAndBounds(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	// Ein Event je Sekunde über deutlich mehr als maxBuckets Sekunden hinweg.
	const span = defaultMaxBuckets*3 + 17
	for i := 0; i < span; i++ {
		h.Add(1, start.Add(time.Duration(i)*time.Second))
	}

	s := h.Snapshot()
	if int(s.Total) != span {
		t.Fatalf("total = %d, want %d (Summe muss bei Kompaktierung erhalten bleiben)", s.Total, span)
	}
	if len(s.Counts) > defaultMaxBuckets {
		t.Fatalf("buckets = %d, want <= %d", len(s.Counts), defaultMaxBuckets)
	}
	if s.Width < 2*time.Second {
		t.Fatalf("width = %v, want >= 2s nach Kompaktierung", s.Width)
	}
	// Summe der Buckets == total.
	var sum uint64
	for _, c := range s.Counts {
		sum += c
	}
	if sum != s.Total {
		t.Fatalf("bucket-summe %d != total %d", sum, s.Total)
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	start := time.Now().UTC()
	h := New(start)
	h.Add(1, start)
	s := h.Snapshot()
	s.Counts[0] = 999
	if s2 := h.Snapshot(); s2.Counts[0] != 1 {
		t.Fatalf("Snapshot teilt internen Speicher: %v", s2.Counts)
	}
}
