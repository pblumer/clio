package store

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func openMode(t *testing.T, mode SyncMode) *Store {
	t.Helper()
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "test.db"), Options{SyncMode: mode})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestGroupCommitConcurrent feuert viele gleichzeitige Writes (die bbolt per
// Group Commit zusammenfasst) und stellt sicher, dass jede Event-ID genau
// einmal und lückenlos 1..N vergeben wird.
func TestGroupCommitConcurrent(t *testing.T) {
	st := openMode(t, SyncGroup)

	const n = 200
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := st.Append([]event.Candidate{
				{Source: "s", Subject: "/c", Type: "t"},
			}, nil)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = got[0].ID
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("append %d: %v", i, errs[i])
		}
		if seen[ids[i]] {
			t.Fatalf("doppelte event-id %q", ids[i])
		}
		seen[ids[i]] = true
	}

	// Lückenlos 1..n im Store.
	all, err := st.Read("/c", false, ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != n {
		t.Fatalf("gespeicherte events = %d, want %d", len(all), n)
	}
	for i, ev := range all {
		want := uint64(i + 1)
		if ev.ID != formatID(want) {
			t.Fatalf("event[%d].id = %q, want %d", i, ev.ID, want)
		}
	}
}

// TestAppendAtomicContiguousIDs: die Events EINES Append-Aufrufs erhalten auch
// unter Group Commit zusammenhängende IDs (ein Aufruf = eine atomare Einheit).
func TestAppendAtomicContiguousIDs(t *testing.T) {
	st := openMode(t, SyncGroup)
	got, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/a", Type: "t1"},
		{Source: "s", Subject: "/a", Type: "t2"},
		{Source: "s", Subject: "/a", Type: "t3"},
	}, nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if got[0].ID != "1" || got[1].ID != "2" || got[2].ID != "3" {
		t.Fatalf("nicht zusammenhängend: %s,%s,%s", got[0].ID, got[1].ID, got[2].ID)
	}
}

// TestSyncModesWriteRead: alle Modi schreiben und lesen korrekt.
func TestSyncModesWriteRead(t *testing.T) {
	for _, mode := range []SyncMode{SyncGroup, SyncAlways, SyncOff} {
		st := openMode(t, mode)
		got, err := st.Append([]event.Candidate{{Source: "s", Subject: "/m", Type: "t"}}, nil)
		if err != nil {
			t.Fatalf("mode %d append: %v", mode, err)
		}
		if got[0].ID != "1" {
			t.Fatalf("mode %d: id = %q, want 1", mode, got[0].ID)
		}
		back, err := st.Read("/m", false, ReadOptions{})
		if err != nil || len(back) != 1 {
			t.Fatalf("mode %d read: %v len=%d", mode, err, len(back))
		}
	}
}

// TestPreconditionUnderGroupCommit: Preconditions bleiben auch im Group-Commit-
// Modus korrekt (der fehlschlagende Write wird aus dem Batch herausgelöst).
func TestPreconditionUnderGroupCommit(t *testing.T) {
	st := openMode(t, SyncGroup)
	pre := []Precondition{{Type: PreconditionSubjectPristine, Subject: "/p"}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/p", Type: "t"}}, pre); err != nil {
		t.Fatalf("erster pristine-write: %v", err)
	}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/p", Type: "t2"}}, pre); !errorsIsPrecondition(err) {
		t.Fatalf("zweiter pristine-write: erwartete ErrPreconditionFailed, bekam %v", err)
	}
}

func formatID(n uint64) string {
	return strconv.FormatUint(n, 10)
}
