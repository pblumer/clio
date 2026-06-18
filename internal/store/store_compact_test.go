package store

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func TestCompactPreservesDataAndChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 200; i++ {
		appendAll(t, st, event.Candidate{
			Source: "s", Subject: "/s", Type: "t",
			Data: json.RawMessage(`{"n":` + itoa(i) + `}`),
		})
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	old, neu, err := Compact(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if old <= 0 || neu <= 0 {
		t.Fatalf("größen unplausibel: old=%d new=%d", old, neu)
	}

	// Nach dem Kompaktieren: Daten und Hash-Kette unverändert.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	if c, _ := st2.Count(); c != 200 {
		t.Fatalf("count nach compact = %d, want 200", c)
	}
	if res, _ := st2.Verify(); !res.OK || res.Count != 200 {
		t.Fatalf("verify nach compact: %+v", res)
	}
}

func TestCompactFailsWhileOpen(t *testing.T) {
	st := openTemp(t)
	if _, _, err := Compact(st.db.Path()); err == nil {
		t.Fatal("Compact bei offener DB sollte fehlschlagen (lock)")
	}
}

func TestSize(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	sz, err := st.Size()
	if err != nil || sz <= 0 {
		t.Fatalf("Size = %d, err = %v", sz, err)
	}
}

func TestStats(t *testing.T) {
	st := openTemp(t)
	for i := 0; i < 50; i++ {
		appendAll(t, st, event.Candidate{
			Source: "s", Subject: "/a", Type: "t",
			Data: json.RawMessage(`{"n":` + itoa(i) + `}`),
		})
	}
	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.FileBytes <= 0 || stats.PageSize <= 0 {
		t.Fatalf("unplausibel: %+v", stats)
	}
	// Invarianten: genutzter Umfang liegt innerhalb der Datei; belegt + frei =
	// genutzter Umfang (nicht die ggf. größere Datei); Füllgrad konsistent.
	if stats.DataBytes <= 0 || stats.DataBytes > stats.FileBytes {
		t.Fatalf("dataBytes außerhalb (0,file]: %+v", stats)
	}
	if stats.UsedBytes+stats.FreeBytes != stats.DataBytes {
		t.Fatalf("used+free != data: %+v", stats)
	}
	if stats.FreeBytes < 0 || stats.FreeBytes > stats.DataBytes {
		t.Fatalf("freeBytes außerhalb [0,data]: %+v", stats)
	}
	if stats.FillPercent < 0 || stats.FillPercent > 100 {
		t.Fatalf("fillPercent außerhalb [0,100]: %+v", stats)
	}
	wantFill := float64(stats.UsedBytes) / float64(stats.DataBytes) * 100
	if d := stats.FillPercent - wantFill; d < -0.001 || d > 0.001 {
		t.Fatalf("fillPercent inkonsistent: %v vs %v", stats.FillPercent, wantFill)
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
