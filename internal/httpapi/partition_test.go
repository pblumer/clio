package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// newPartitionedServer baut einen Test-Server mit N Partitionen (ADR-034/037).
func newPartitionedServer(t *testing.T, n int) *Server {
	t.Helper()
	st, err := store.OpenWithOptions(filepath.Join(t.TempDir(), "test.db"),
		store.Options{SyncMode: store.SyncGroup, Partitions: n})
	if err != nil {
		t.Fatalf("partitionierten store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedKey(t, st, testAdminKID, testAdminSecret, auth.StatusActive, auth.ScopeRead, auth.ScopeWrite, auth.ScopeAdmin)
	return New(config.Config{Addr: ":0"}, st, nil)
}

// twoSourcesInDistinctPartitions liefert zwei Sources, die auf verschiedene
// Partitionen abbilden.
func twoSourcesInDistinctPartitions(srv *Server) (a, b string, ok bool) {
	a = "/svc/a"
	pa := srv.store.PartitionOf(a)
	for i := 0; i < 10000; i++ {
		b = fmt.Sprintf("/svc/b-%d", i)
		if srv.store.PartitionOf(b) != pa {
			return a, b, true
		}
	}
	return "", "", false
}

// TestPartitionedReadEventsCarriesPartition: bei N>1 trägt jedes über die HTTP-API
// gelesene Event das `partition`-Sicht-Attribut passend zur Routing-Partition —
// die Grundlage des per-Partition-Cursors für Konsumenten (ADR-036).
func TestPartitionedReadEventsCarriesPartition(t *testing.T) {
	srv := newPartitionedServer(t, 4)
	a, b, ok := twoSourcesInDistinctPartitions(srv)
	if !ok {
		t.Skip("keine zwei Sources in verschiedenen Partitionen")
	}
	for _, src := range []string{a, b} {
		body := fmt.Sprintf(`{"events":[{"source":%q,"subject":"/p/x","type":"t"}]}`, src)
		if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body); rec.Code != http.StatusOK {
			t.Fatalf("write %s = %d; body=%s", src, rec.Code, rec.Body.String())
		}
	}

	rec := do(t, srv, http.MethodPost, "/api/v1/read-events", adminToken, `{"subject":"/p/x"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-events = %d; body=%s", rec.Code, rec.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("erwartet 2 Events (partitionsübergreifend), bekam %d: %s", len(lines), rec.Body.String())
	}
	sawNonZero := false
	for _, ln := range lines {
		var ev event.Event
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("event dekodieren: %v (%s)", err, ln)
		}
		if want := srv.store.PartitionOf(ev.Source); ev.Partition != want {
			t.Errorf("source %s: partition=%d, want %d", ev.Source, ev.Partition, want)
		}
		if ev.Partition != 0 {
			sawNonZero = true
		}
	}
	if !sawNonZero {
		t.Error("erwartet mindestens ein Event in einer Partition != 0")
	}
}

// TestPartitionedObserveCursorOutOfRange: ein Cursor mit einer Partition außerhalb
// 0..N-1 wird mit 400 abgelehnt (statt still ignoriert).
func TestPartitionedObserveCursorOutOfRange(t *testing.T) {
	srv := newPartitionedServer(t, 4)
	body := `{"subject":"/x","cursor":{"9":1}}`
	rec := do(t, srv, http.MethodPost, "/api/v1/observe-events", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("observe mit ungültigem Cursor = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPartitionedMixedWriteBatchRejected: ein write-events-Batch mit Sources aus
// verschiedenen Partitionen wird über die HTTP-API mit 400 abgelehnt (ADR-034).
func TestPartitionedMixedWriteBatchRejected(t *testing.T) {
	srv := newPartitionedServer(t, 8)
	a, b, ok := twoSourcesInDistinctPartitions(srv)
	if !ok {
		t.Skip("keine zwei Sources in verschiedenen Partitionen")
	}
	body := fmt.Sprintf(`{"events":[{"source":%q,"subject":"/m","type":"t"},{"source":%q,"subject":"/m","type":"t"}]}`, a, b)
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("gemischter Batch = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
