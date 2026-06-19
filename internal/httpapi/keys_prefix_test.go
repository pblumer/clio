package httpapi

import (
	"net/http"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

// TestPrefixScopeEnforcement deckt ADR-033 ab: ein subject-gebundener Schlüssel
// darf nur in seinem Teilbaum lesen/schreiben; alles außerhalb sowie
// aggregat-/globale Routen ergeben 403. Ein globaler Schlüssel bleibt unberührt.
func TestPrefixScopeEnforcement(t *testing.T) {
	srv := newTestServer(t)

	// Etwas Bestand anlegen (mit dem globalen Admin).
	if rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken,
		`{"events":[{"source":"s","subject":"/orders/o-1","type":"order.placed"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("seed write status = %d", rec.Code)
	}

	// Prefix-Schlüssel: read+write nur auf /orders/*.
	tok := seedKey(t, srv.store, "kid_ordwr", "ordersorderorderorderorder012345",
		auth.StatusActive, auth.Scope("read:/orders/*"), auth.Scope("write:/orders/*"))

	type tc struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}
	cases := []tc{
		// Schreiben im Teilbaum erlaubt, außerhalb verboten (jedes Event geprüft).
		{"write innerhalb", http.MethodPost, "/api/v1/write-events", `{"events":[{"source":"s","subject":"/orders/o-2","type":"t"}]}`, http.StatusOK},
		{"write außerhalb", http.MethodPost, "/api/v1/write-events", `{"events":[{"source":"s","subject":"/customers/c-1","type":"t"}]}`, http.StatusForbidden},
		{"write gemischt -> atomar 403", http.MethodPost, "/api/v1/write-events", `{"events":[{"source":"s","subject":"/orders/o-3","type":"t"},{"source":"s","subject":"/customers/c-9","type":"t"}]}`, http.StatusForbidden},
		// Lesen im Teilbaum erlaubt, außerhalb / Wurzel verboten.
		{"read innerhalb", http.MethodPost, "/api/v1/read-events", `{"subject":"/orders","recursive":true}`, http.StatusOK},
		{"read exakt im teilbaum", http.MethodPost, "/api/v1/read-events", `{"subject":"/orders/o-1"}`, http.StatusOK},
		{"read außerhalb", http.MethodPost, "/api/v1/read-events", `{"subject":"/customers","recursive":true}`, http.StatusForbidden},
		{"read wurzel rekursiv", http.MethodPost, "/api/v1/read-events", `{"subject":"/","recursive":true}`, http.StatusForbidden},
		// observe: Deny-Pfad greift vor dem Streaming.
		{"observe außerhalb", http.MethodPost, "/api/v1/observe-events", `{"subject":"/customers","recursive":true}`, http.StatusForbidden},
		// run-query.
		{"query innerhalb", http.MethodPost, "/api/v1/run-query", `{"subject":"/orders","recursive":true}`, http.StatusOK},
		{"query wurzel", http.MethodPost, "/api/v1/run-query", `{"subject":"/","recursive":true}`, http.StatusForbidden},
		// read-subjects: Prefix im Teilbaum ok, ohne Prefix (= ganzer Baum) verboten.
		{"read-subjects prefix", http.MethodGet, "/api/v1/read-subjects?prefix=/orders", "", http.StatusOK},
		{"read-subjects ohne prefix", http.MethodGet, "/api/v1/read-subjects", "", http.StatusForbidden},
		// GET-Pfad-Route.
		{"events-path innerhalb", http.MethodGet, "/api/v1/events/orders/o-1?recursive=false", "", http.StatusOK},
		{"events-path außerhalb", http.MethodGet, "/api/v1/events/customers", "", http.StatusForbidden},
		// Aggregat-/globale Routen verlangen globalen read.
		{"info global", http.MethodGet, "/api/v1/info", "", http.StatusForbidden},
		{"event-stats global", http.MethodGet, "/api/v1/event-stats", "", http.StatusForbidden},
		{"read-event-types global", http.MethodGet, "/api/v1/read-event-types", "", http.StatusForbidden},
		{"verify global", http.MethodGet, "/api/v1/verify", "", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, srv, c.method, c.path, tok, c.body)
			if rec.Code != c.want {
				t.Fatalf("%s %s -> %d, want %d; body=%s", c.method, c.path, rec.Code, c.want, rec.Body.String())
			}
		})
	}

	// Backward-Compat-Gegenprobe: ein GLOBALER read-Schlüssel darf die
	// Aggregat-Routen und beliebige Subjects lesen.
	gtok := seedKey(t, srv.store, "kid_grdr", "globalglobalglobalglobalglobal01",
		auth.StatusActive, auth.ScopeRead)
	for _, p := range []string{"/api/v1/info", "/api/v1/verify", "/api/v1/read-event-types"} {
		if rec := do(t, srv, http.MethodGet, p, gtok, ""); rec.Code != http.StatusOK {
			t.Fatalf("globaler read %s -> %d, want 200", p, rec.Code)
		}
	}
	if rec := do(t, srv, http.MethodPost, "/api/v1/read-events", gtok, `{"subject":"/","recursive":true}`); rec.Code != http.StatusOK {
		t.Fatalf("globaler read root -> %d, want 200", rec.Code)
	}
}

// TestCreateKeyWithPrefixScope: die Key-Anlage akzeptiert die neue Grammatik und
// lehnt Unsinn ab.
func TestCreateKeyWithPrefixScope(t *testing.T) {
	srv := newTestServer(t)
	// Gültig: read:/orders/* + write:/orders/*
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"orders-rw","scopes":["read:/orders/*","write:/orders/*"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("create prefix-key status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Ungültig: admin darf nicht subject-gebunden sein.
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"bad","scopes":["admin:/orders/*"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("admin:/orders/* sollte 400 sein, ist %d", rec.Code)
	}
	// Ungültig: unbekannte Aktion.
	if rec := do(t, srv, http.MethodPost, "/api/v1/keys", adminToken,
		`{"name":"bad2","scopes":["observe:/orders/*"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("observe:/orders/* sollte 400 sein, ist %d", rec.Code)
	}
}
