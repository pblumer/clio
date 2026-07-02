package store

import (
	"strconv"
	"sync"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// TestPublishInSequenceOrder ist der Regressionstest zu K2: der Store muss
// committete Events streng in Sequenzreihenfolge an die Live-Senke liefern, auch
// unter starker Schreib-Nebenläufigkeit. Vor dem Fix lief das Publish aus der
// Handler-Goroutine — zwei nebenläufige Writer konnten ihr Publish gegenüber der
// Commit-Reihenfolge vertauschen, worauf die per-Partition-Dedup der Observer die
// früher committeten Events dauerhaft als Duplikate verwarf (Event-Verlust im
// Live-Stream).
//
// Der Test hängt eine Senke an, die die Auslieferungsreihenfolge protokolliert,
// feuert viele Single-Event-Appends nebenläufig ab und prüft danach: (a) es geht
// kein Event verloren, (b) die Auslieferung ist lückenlos aufsteigend 1..N — also
// exakt die Commit-Reihenfolge, keine Vertauschung.
func TestPublishInSequenceOrder(t *testing.T) {
	st := openTemp(t)

	var mu sync.Mutex
	var delivered []uint64
	st.SetPublisher(func(evs []event.Event) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range evs {
			seq, err := strconv.ParseUint(ev.ID, 10, 64)
			if err != nil {
				t.Errorf("nicht-numerische event-id %q", ev.ID)
				return
			}
			delivered = append(delivered, seq)
		}
	})

	const n = 500
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			// Alle in dieselbe Partition (eine Source) — so ist die erwartete
			// Auslieferungsreihenfolge eindeutig 1..N.
			if _, err := st.Append([]event.Candidate{
				{Source: "loadgen", Subject: "/k2", Type: "io.k2.tick"},
			}, nil); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(delivered) != n {
		t.Fatalf("ausgelieferte Events = %d, want %d (Event-Verlust im Live-Publish?)", len(delivered), n)
	}
	for i, seq := range delivered {
		if want := uint64(i + 1); seq != want {
			t.Fatalf("Auslieferung[%d] = %d, want %d — Publish nicht in Sequenzreihenfolge (K2)", i, seq, want)
		}
	}
}

// TestPublishOrderMultiEventBatches prüft die Reihenfolge zusätzlich mit
// Mehrfach-Batches je Append: jeder Batch belegt eine zusammenhängende
// Sequenzspanne, und die Spannen müssen lückenlos und aufsteigend ausgeliefert
// werden.
func TestPublishOrderMultiEventBatches(t *testing.T) {
	st := openTemp(t)

	var mu sync.Mutex
	var delivered []uint64
	st.SetPublisher(func(evs []event.Event) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range evs {
			seq, _ := strconv.ParseUint(ev.ID, 10, 64)
			delivered = append(delivered, seq)
		}
	})

	const batches = 200
	const perBatch = 3
	var wg sync.WaitGroup
	wg.Add(batches)
	for i := 0; i < batches; i++ {
		go func() {
			defer wg.Done()
			cands := make([]event.Candidate, perBatch)
			for j := range cands {
				cands[j] = event.Candidate{Source: "loadgen", Subject: "/k2b", Type: "io.k2.tick"}
			}
			if _, err := st.Append(cands, nil); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(delivered) != batches*perBatch {
		t.Fatalf("ausgelieferte Events = %d, want %d", len(delivered), batches*perBatch)
	}
	for i, seq := range delivered {
		if want := uint64(i + 1); seq != want {
			t.Fatalf("Auslieferung[%d] = %d, want %d — Batch-Spannen nicht in Reihenfolge (K2)", i, seq, want)
		}
	}
}
