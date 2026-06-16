package store

import (
	"fmt"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

func openTempOpts(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := OpenWithOptions(path, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

// realisticCandidates erzeugt n Events mit langen Subjects/Types und JSON-Daten —
// repräsentativ für einen typischen Eventstrom (vgl. ADR-024).
func realisticCandidates(n int) []event.Candidate {
	cands := make([]event.Candidate, n)
	for i := 0; i < n; i++ {
		cands[i] = event.Candidate{
			Source:  "https://erp.example.com/services/identity",
			Subject: fmt.Sprintf("/scenarios/identity_ops/employees/E-%06d", i),
			Type:    "identity.employee.mailbox.attached",
			Data:    []byte(fmt.Sprintf(`{"employeeId":"E-%06d","mailbox":"user%d@example.com","quotaMb":51200,"region":"eu-central-1"}`, i, i)),
		}
	}
	return cands
}

// TestCompressedRoundTripAndVerify schreibt mit aktiver Kompression, liest die
// Events unverändert zurück und prüft, dass die Hash-Kette intakt bleibt.
func TestCompressedRoundTripAndVerify(t *testing.T) {
	st, _ := openTempOpts(t, Options{SyncMode: SyncGroup, Compress: true})

	want := appendAll(t, st, realisticCandidates(50)...)

	got, err := st.Read("/", true, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("read len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Hash != want[i].Hash || got[i].Subject != want[i].Subject || got[i].Type != want[i].Type {
			t.Fatalf("event[%d] weicht ab: got %+v want %+v", i, got[i], want[i])
		}
		if string(got[i].Data) != string(want[i].Data) {
			t.Fatalf("event[%d] data weicht ab: got %s want %s", i, got[i].Data, want[i].Data)
		}
	}

	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK {
		t.Fatalf("verify nicht ok: %+v", res)
	}
}

// TestMixedCompressedAndRaw stellt sicher, dass eine Datenbank mit gemischten
// Werten (erst roh geschrieben, dann mit Kompression weitergeschrieben) korrekt
// gelesen und verifiziert wird — der Kern der Abwärtskompatibilität.
func TestMixedCompressedAndRaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.db")

	st1, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, Compress: false})
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	appendAll(t, st1, realisticCandidates(10)...)
	if err := st1.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	st2, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, Compress: true})
	if err != nil {
		t.Fatalf("reopen compress: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	appendAll(t, st2, realisticCandidates(10)...)

	got, err := st2.Read("/", true, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("read len = %d, want 20", len(got))
	}
	res, err := st2.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK {
		t.Fatalf("verify nicht ok über gemischte DB: %+v", res)
	}
}

// TestOnDiskSizeReduction schreibt denselben Eventstrom in zwei Datenbanken (roh
// vs. komprimiert) und vergleicht die Summe der tatsächlich gespeicherten
// Event-Wert-Bytes. Das misst die Nutzlast direkt — unabhängig von bbolts
// seitenweiser/2er-Potenz-Granularität der Dateigröße. Dokumentierter
// Größennachweis (sichtbar mit `go test -v`).
func TestOnDiskSizeReduction(t *testing.T) {
	const n = 2000
	cands := realisticCandidates(n)

	rawSize := eventValueBytes(t, "raw.db", false, cands)
	compSize := eventValueBytes(t, "comp.db", true, cands)

	t.Logf("Event-Wert-Bytes für %d Events: roh %d B, komprimiert %d B (%.0f%% der Größe, -%.0f%%)",
		n, rawSize, compSize, 100*float64(compSize)/float64(rawSize), 100*(1-float64(compSize)/float64(rawSize)))

	if compSize >= rawSize {
		t.Fatalf("komprimierte Ablage nicht kleiner: %d >= %d", compSize, rawSize)
	}
}

// eventValueBytes schreibt cands und summiert die Bytes aller Werte im
// events-Bucket (die eigentliche Event-Nutzlast auf Platte).
func eventValueBytes(t *testing.T, name string, compress bool, cands []event.Candidate) int64 {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	st, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, Compress: compress})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Append(cands, nil); err != nil {
		t.Fatalf("append %s: %v", name, err)
	}

	var total int64
	err = st.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketEvents).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			total += int64(len(v))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", name, err)
	}
	return total
}
