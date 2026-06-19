package auth

import "testing"

func TestParseGrant(t *testing.T) {
	cases := []struct {
		in        string
		wantErr   bool
		action    Scope
		subject   string
		recursive bool
	}{
		{"read", false, ScopeRead, "", false},
		{"write", false, ScopeWrite, "", false},
		{"admin", false, ScopeAdmin, "", false},
		{"audit", false, ScopeAudit, "", false},
		{"read:/orders", false, ScopeRead, "/orders", false},
		{"read:/orders/*", false, ScopeRead, "/orders", true},
		{"write:/orders/*", false, ScopeWrite, "/orders", true},
		{"read:/*", false, ScopeRead, "/", true},
		{"read:/orders/", false, ScopeRead, "/orders", false}, // Trailing-Slash normalisiert
		// Fehlerfälle:
		{"bogus", true, "", "", false},
		{"admin:/orders/*", true, "", "", false}, // admin nicht subject-gebunden
		{"audit:/x", true, "", "", false},
		{"read:orders", true, "", "", false}, // muss mit / beginnen
		{"read:/a/*/b", true, "", "", false}, // * nur als Suffix
		{"read:", true, "", "", false},       // leeres Subject
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			g, err := ParseGrant(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseGrant(%q) = %+v, want error", tc.in, g)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseGrant(%q): %v", tc.in, err)
			}
			if g.Action != tc.action || g.Subject != tc.subject || g.Recursive != tc.recursive {
				t.Fatalf("ParseGrant(%q) = %+v, want {%s %s %v}", tc.in, g, tc.action, tc.subject, tc.recursive)
			}
		})
	}
}

func TestScopesAllow(t *testing.T) {
	cases := []struct {
		name      string
		scopes    []Scope
		action    Scope
		subject   string
		recursive bool
		want      bool
	}{
		{"global read deckt alles", []Scope{ScopeRead}, ScopeRead, "/orders/o-1", false, true},
		{"global read deckt rekursiv root", []Scope{ScopeRead}, ScopeRead, "/", true, true},
		{"prefix deckt kind", []Scope{"read:/orders/*"}, ScopeRead, "/orders/o-1", false, true},
		{"prefix deckt sich selbst", []Scope{"read:/orders/*"}, ScopeRead, "/orders", false, true},
		{"prefix deckt rekursiv im teilbaum", []Scope{"read:/orders/*"}, ScopeRead, "/orders/eu", true, true},
		{"prefix deckt NICHT außerhalb", []Scope{"read:/orders/*"}, ScopeRead, "/customers/c-1", false, false},
		{"prefix deckt NICHT geschwister", []Scope{"read:/orders/*"}, ScopeRead, "/ordersX", false, false},
		{"prefix deckt NICHT root-rekursiv", []Scope{"read:/orders/*"}, ScopeRead, "/", true, false},
		{"exaktes subject nur exakt", []Scope{"read:/orders"}, ScopeRead, "/orders", false, true},
		{"exaktes subject deckt kind NICHT", []Scope{"read:/orders"}, ScopeRead, "/orders/o-1", false, false},
		{"falsche aktion", []Scope{"read:/orders/*"}, ScopeWrite, "/orders/o-1", false, false},
		{"rekursive anfrage braucht rekursiven grant", []Scope{"read:/orders"}, ScopeRead, "/orders", true, false},
		{"root-grant deckt alles", []Scope{"read:/*"}, ScopeRead, "/anything/deep", false, true},
		{"root-grant deckt rekursiv-root", []Scope{"read:/*"}, ScopeRead, "/", true, true},
		{"mehrere grants, einer passt", []Scope{"read:/a/*", "read:/orders/*"}, ScopeRead, "/orders/o-1", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopesAllow(tc.scopes, tc.action, tc.subject, tc.recursive); got != tc.want {
				t.Fatalf("ScopesAllow(%v,%s,%s,%v) = %v, want %v", tc.scopes, tc.action, tc.subject, tc.recursive, got, tc.want)
			}
		})
	}
}

func TestHasAction(t *testing.T) {
	k := Key{Scopes: []Scope{"read:/orders/*", "write:/orders/*"}}
	if !k.HasAction(ScopeRead) || !k.HasAction(ScopeWrite) {
		t.Fatal("prefix-key sollte read- und write-Aktion tragen")
	}
	if k.HasAction(ScopeAdmin) {
		t.Fatal("prefix-key sollte keine admin-Aktion tragen")
	}
	// HasScope bleibt exakt (global): "read" != "read:/orders/*"
	if k.HasScope(ScopeRead) {
		t.Fatal("HasScope(read) darf bei reinem prefix-grant nicht greifen")
	}
}
