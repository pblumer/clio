package query

import "testing"

func TestDataEqualities(t *testing.T) {
	c := mustCompiler(t)
	tests := []struct {
		name string
		expr string
		want []DataEquality
	}{
		{
			name: "einfache Gleichheit",
			expr: `event.data.department == 'support'`,
			want: []DataEquality{{Field: "department", Value: "support"}},
		},
		{
			name: "umgekehrte Operanden",
			expr: `'support' == event.data.department`,
			want: []DataEquality{{Field: "department", Value: "support"}},
		},
		{
			name: "mit Typ-Constraint (UND)",
			expr: `event.type == 'emp.v2' && event.data.department == 'support'`,
			want: []DataEquality{{Field: "department", Value: "support"}},
		},
		{
			name: "mehrere UND-Gleichheiten",
			expr: `event.data.a == 'x' && event.data.b == 'y'`,
			want: []DataEquality{{Field: "a", Value: "x"}, {Field: "b", Value: "y"}},
		},
		{
			name: "ODER liefert nichts (nicht notwendig)",
			expr: `event.data.a == 'x' || event.data.b == 'y'`,
			want: nil,
		},
		{
			name: "nicht-String-Wert ignoriert",
			expr: `event.data.amount == 100`,
			want: nil,
		},
		{
			name: "verschachtelter Pfad nicht abgedeckt (v1)",
			expr: `event.data.a.b == 'x'`,
			want: nil,
		},
		{
			name: "Vergleich ohne Gleichheit",
			expr: `event.data.amount > 100`,
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := c.Compile(tc.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got := p.DataEqualities()
			if len(got) != len(tc.want) {
				t.Fatalf("DataEqualities() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("DataEqualities()[%d] = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
