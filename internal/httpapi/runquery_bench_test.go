package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

// BenchmarkRunQueryTypeFilter misst die run-query-Latenz für einen reinen
// Typ-Filter über einen großen Scope (`/` rekursiv). Die Treffer liegen — wie im
// gemeldeten Fall — am Ende, sodass praktisch der ganze Store gescannt wird.
func BenchmarkRunQueryTypeFilter(b *testing.B) {
	const total = 150000
	const matches = 200

	st, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })

	data, _ := json.Marshal(map[string]any{
		"rentalId": "R-LIVE-01000", "customer": "Alice Example", "amount": 249.9,
		"currency": "CHF", "items": []string{"car", "insurance", "gps"},
		"address": map[string]any{"street": "Musterweg 1", "city": "Zürich", "zip": "8000"},
	})
	// In Blöcken anhängen (eine Transaktion je Block). Die letzten `matches`
	// Events tragen den gesuchten Typ.
	const block = 2000
	for start := 0; start < total; start += block {
		cands := make([]event.Candidate, 0, block)
		for i := start; i < start+block && i < total; i++ {
			typ := "rental.created"
			if i >= total-matches {
				typ = "afternoon.invoice.sent"
			}
			cands = append(cands, event.Candidate{
				Source: "bench", Subject: fmt.Sprintf("/scenarios/live/rentals/R-%06d", i),
				Type: typ, Data: json.RawMessage(data),
			})
		}
		if _, err := st.Append(cands, nil); err != nil {
			b.Fatal(err)
		}
	}

	bk := auth.Key{KID: "kid_bench", Name: "bench", SecretHash: auth.HashSecret("benchsecret"), Scopes: []auth.Scope{auth.ScopeRead}, Status: auth.StatusActive}
	if err := st.PutKey(bk); err != nil {
		b.Fatal(err)
	}
	srv := New(config.Config{Addr: ":0"}, st, nil)
	body := `{"subject":"/","recursive":true,"where":"event.type == 'afternoon.invoice.sent'","limit":100}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/run-query", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer kid_bench.benchsecret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
}
