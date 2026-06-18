package query

import (
	"reflect"
	"testing"
)

func TestRequiredTypes(t *testing.T) {
	c, err := NewCompiler()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		expr      string
		wantTypes []string
		wantOK    bool
	}{
		// Einfache Gleichheit (beide Schreibrichtungen).
		{"event.type == 'order-placed'", []string{"order-placed"}, true},
		{"'order-placed' == event.type", []string{"order-placed"}, true},
		// Typ-Constraint als notwendiger UND-Teil.
		{"event.type == 'order-placed' && event.data.amount > 100", []string{"order-placed"}, true},
		{"event.data.amount > 100 && event.type == 'x'", []string{"x"}, true},
		// in-Liste.
		{"event.type in ['a', 'b']", []string{"a", "b"}, true},
		// ODER zweier Typ-Constraints -> Vereinigung.
		{"event.type == 'a' || event.type == 'b'", []string{"a", "b"}, true},
		// Widersprüchliches UND -> leere Menge, aber sicher bestimmt.
		{"event.type == 'a' && event.type == 'b'", []string{}, true},
		// NICHT einschränkbar (Full-Scan nötig):
		{"event.data.amount > 0", nil, false},                      // kein Typbezug
		{"event.type != 'a'", nil, false},                          // Ungleichheit
		{"event.type == 'a' || event.subject == '/x'", nil, false}, // ODER mit unbeschränkter Seite
		{"event.subject == '/x' || event.type == 'a'", nil, false}, // dito, andere Seite
		{"has(event.data.x) || event.type == 'a'", nil, false},     // dito
		{"event.type == 'a' || event.data.amount > 0", nil, false}, // dito
		// RHS ist kein String-Literal (sondern ein Feldzugriff) -> nicht ableitbar.
		{"event.type == event.source", nil, false},
		// in-Liste mit einem Nicht-Literal-Element -> Menge nicht bestimmbar.
		{"event.type in ['a', event.source]", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			p, err := c.Compile(tt.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, ok := p.RequiredTypes()
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (types=%v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if tt.wantTypes == nil {
				tt.wantTypes = []string{}
			}
			if !reflect.DeepEqual(got, tt.wantTypes) {
				t.Fatalf("types = %v, want %v", got, tt.wantTypes)
			}
		})
	}
}
