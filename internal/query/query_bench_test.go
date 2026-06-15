package query

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// benchEvents baut n Events mit einem nicht-trivialen data-Payload (wie in
// realen Szenarien), damit das Parsen von data spürbar ins Gewicht fällt.
func benchEvents(n int) []event.Event {
	data, _ := json.Marshal(map[string]any{
		"rentalId": "R-LIVE-01000", "customer": "Alice Example", "amount": 249.9,
		"currency": "CHF", "items": []string{"car", "insurance", "gps"},
		"address": map[string]any{"street": "Musterweg 1", "city": "Zürich", "zip": "8000"},
		"note":    "afternoon invoice for the live story scenario",
	})
	evs := make([]event.Event, n)
	for i := 0; i < n; i++ {
		evs[i] = event.Event{
			ID:      fmt.Sprintf("%d", i+1),
			Source:  "bench",
			Subject: "/scenarios/autoverleihung/live-story/rentals/R-LIVE-01000",
			Type:    "afternoon.invoice.sent",
			Data:    json.RawMessage(data),
		}
	}
	return evs
}

func benchEval(b *testing.B, expr string) {
	c, err := NewCompiler()
	if err != nil {
		b.Fatal(err)
	}
	p, err := c.Compile(expr)
	if err != nil {
		b.Fatal(err)
	}
	evs := benchEvents(2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range evs {
			if _, err := p.Eval(evs[j]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// Typ-Filter: referenziert data NICHT -> data wird nicht geparst.
func BenchmarkEvalTypeOnly(b *testing.B) { benchEval(b, "event.type == 'afternoon.invoice.sent'") }

// data-Filter: referenziert data -> data wird je Event geparst (Vergleichswert).
func BenchmarkEvalWithData(b *testing.B) {
	benchEval(b, "has(event.data.amount) && event.data.amount > 100")
}
