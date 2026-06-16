package eventstats

import (
	"testing"
	"time"
)

func TestAddAndSnapshot(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	h.Add(2, start)                           // Bucket 0
	h.Add(3, start.Add(500*time.Millisecond)) // ebenfalls Bucket 0 (1s breit)
	h.Add(1, start.Add(2*time.Second))        // Bucket 2

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

func TestAddSourceTracksSeriesAndTotal(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	h.AddSource(2, start, "svc-a")                    // Bucket 0
	h.AddSource(3, start.Add(2*time.Second), "svc-a") // Bucket 2
	h.AddSource(5, start, "svc-b")                    // Bucket 0

	s := h.SnapshotBySource()
	if s.Total != 10 {
		t.Fatalf("total = %d, want 10", s.Total)
	}
	// Gesamthistogramm: Bucket 0 = 7 (2+5), Bucket 2 = 3.
	if len(s.Counts) != 3 || s.Counts[0] != 7 || s.Counts[2] != 3 {
		t.Fatalf("counts = %v, want [7 0 3]", s.Counts)
	}
	a := s.Sources["svc-a"]
	if len(a) != 3 || a[0] != 2 || a[2] != 3 {
		t.Fatalf("svc-a = %v, want [2 0 3]", a)
	}
	b := s.Sources["svc-b"]
	if len(b) != 1 || b[0] != 5 {
		t.Fatalf("svc-b = %v, want [5]", b)
	}

	// Pro Bucket muss die Summe der Serien dem Gesamthistogramm entsprechen.
	assertSeriesSumToCounts(t, s)
}

func TestSourceSeriesStayAlignedAfterCompaction(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	const span = defaultMaxBuckets*3 + 17
	for i := 0; i < span; i++ {
		src := "even"
		if i%2 == 1 {
			src = "odd"
		}
		h.AddSource(1, start.Add(time.Duration(i)*time.Second), src)
	}

	s := h.SnapshotBySource()
	if int(s.Total) != span {
		t.Fatalf("total = %d, want %d", s.Total, span)
	}
	if len(s.Counts) > defaultMaxBuckets {
		t.Fatalf("buckets = %d, want <= %d", len(s.Counts), defaultMaxBuckets)
	}
	// Auch nach mehrfacher Kompaktierung müssen die Serien zu counts ausgerichtet
	// bleiben (bucketweise Summe == Gesamt) und die Einzelsummen erhalten sein.
	assertSeriesSumToCounts(t, s)
	var even, odd uint64
	for _, c := range s.Sources["even"] {
		even += c
	}
	for _, c := range s.Sources["odd"] {
		odd += c
	}
	if even != (span+1)/2 || odd != span/2 {
		t.Fatalf("even=%d odd=%d, want even=%d odd=%d", even, odd, (span+1)/2, span/2)
	}
}

func TestSourceOverflowBeyondLimit(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)

	// maxSources verschiedene Sources + viele weitere → die weiteren landen im
	// Overflow-Schlüssel; die Serienzahl bleibt beschränkt.
	for i := 0; i < defaultMaxSources; i++ {
		h.AddSource(1, start, "src-"+itoa(i))
	}
	for i := 0; i < 50; i++ {
		h.AddSource(1, start, "extra-"+itoa(i))
	}

	s := h.SnapshotBySource()
	if len(s.Sources) != defaultMaxSources+1 {
		t.Fatalf("serien = %d, want %d (maxSources + Overflow)", len(s.Sources), defaultMaxSources+1)
	}
	ov := s.Sources[OverflowSource]
	if len(ov) != 1 || ov[0] != 50 {
		t.Fatalf("overflow = %v, want [50]", ov)
	}
	assertSeriesSumToCounts(t, s)
}

func TestResetClearsSeries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := New(start)
	h.AddSource(3, start, "svc-a")
	h.Reset(start)
	s := h.SnapshotBySource()
	if s.Total != 0 || len(s.Counts) != 0 || len(s.Sources) != 0 {
		t.Fatalf("nach Reset nicht leer: %+v", s)
	}
}

// assertSeriesSumToCounts prüft bucketweise: Summe aller Source-Serien == Counts.
func assertSeriesSumToCounts(t *testing.T, s SourceSnapshot) {
	t.Helper()
	sum := make([]uint64, len(s.Counts))
	for _, series := range s.Sources {
		for i, v := range series {
			sum[i] += v
		}
	}
	for i := range s.Counts {
		if sum[i] != s.Counts[i] {
			t.Fatalf("bucket %d: serien-summe %d != counts %d", i, sum[i], s.Counts[i])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
