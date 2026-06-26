package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/pblumer/clio/internal/auth"
)

// registerSpec registriert eine Reduce-Spec für einen Prefix und erwartet 200.
func registerSpec(t *testing.T, srv *Server, prefix, specJSON string) {
	t.Helper()
	body := `{"prefix":"` + prefix + `","spec":` + specJSON + `}`
	rec := do(t, srv, http.MethodPost, "/api/v1/register-reduce-spec", adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("register-reduce-spec (%s) status = %d, want 200; body=%s", prefix, rec.Code, rec.Body.String())
	}
}

// TestReduceStrategies prüft sum/min/max/append/union/first über mehrere Events.
func TestReduceStrategies(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{
		"fields":{
			"amount":"sum",
			"low":"min",
			"high":"max",
			"log":"append",
			"tags":"union",
			"createdBy":"first",
			"stats.views":"sum"
		}
	}`)

	writeEvent(t, srv, "/orders/1", "a", `{"amount":100,"low":5,"high":5,"log":"x","tags":["red"],"createdBy":"alice","stats":{"views":1}}`)
	writeEvent(t, srv, "/orders/1", "b", `{"amount":50,"low":3,"high":8,"log":["y","z"],"tags":["red","blue"],"createdBy":"bob","stats":{"views":2}}`)
	writeEvent(t, srv, "/orders/1", "c", `{"amount":25,"low":9,"high":2,"log":"w","tags":["green"],"stats":{"views":3}}`)

	resp, code := getState(t, srv, "orders/1")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Reducer != "/orders" {
		t.Errorf("reducer = %q, want /orders", resp.Reducer)
	}
	if resp.State["amount"] != float64(175) {
		t.Errorf("amount (sum) = %v, want 175", resp.State["amount"])
	}
	if resp.State["low"] != float64(3) {
		t.Errorf("low (min) = %v, want 3", resp.State["low"])
	}
	if resp.State["high"] != float64(8) {
		t.Errorf("high (max) = %v, want 8", resp.State["high"])
	}
	if resp.State["createdBy"] != "alice" {
		t.Errorf("createdBy (first) = %v, want alice", resp.State["createdBy"])
	}
	log, _ := resp.State["log"].([]any)
	if len(log) != 4 || log[0] != "x" || log[1] != "y" || log[2] != "z" || log[3] != "w" {
		t.Errorf("log (append) = %v, want [x y z w]", resp.State["log"])
	}
	tags, _ := resp.State["tags"].([]any)
	if len(tags) != 3 {
		t.Errorf("tags (union) = %v, want 3 distinct (red blue green)", resp.State["tags"])
	}
	stats, _ := resp.State["stats"].(map[string]any)
	if stats == nil || stats["views"] != float64(6) {
		t.Errorf("stats.views (sum) = %v, want 6", resp.State["stats"])
	}
}

// TestReduceLongestPrefix: bei mehreren passenden Prefixen gewinnt der längste.
func TestReduceLongestPrefix(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"sum"}}`)
	registerSpec(t, srv, "/orders/special", `{"fields":{"amount":"max"}}`)

	writeEvent(t, srv, "/orders/normal", "x", `{"amount":10}`)
	writeEvent(t, srv, "/orders/normal", "x", `{"amount":10}`)
	writeEvent(t, srv, "/orders/special", "x", `{"amount":10}`)
	writeEvent(t, srv, "/orders/special", "x", `{"amount":10}`)

	normal, _ := getState(t, srv, "orders/normal")
	if normal.Reducer != "/orders" || normal.State["amount"] != float64(20) {
		t.Errorf("normal: reducer=%q amount=%v, want /orders & 20 (sum)", normal.Reducer, normal.State["amount"])
	}
	special, _ := getState(t, srv, "orders/special")
	if special.Reducer != "/orders/special" || special.State["amount"] != float64(10) {
		t.Errorf("special: reducer=%q amount=%v, want /orders/special & 10 (max)", special.Reducer, special.State["amount"])
	}
}

// TestReduceDefaultStrategy: ein nicht-lww-Default greift auf alle Felder.
func TestReduceDefaultStrategy(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/counters", `{"default":"sum"}`)
	writeEvent(t, srv, "/counters/c1", "x", `{"a":1,"b":10}`)
	writeEvent(t, srv, "/counters/c1", "x", `{"a":2,"b":20}`)

	resp, _ := getState(t, srv, "counters/c1")
	if resp.State["a"] != float64(3) || resp.State["b"] != float64(30) {
		t.Errorf("default sum: a=%v b=%v, want 3/30", resp.State["a"], resp.State["b"])
	}
}

// TestStateCacheIncremental: nach weiteren Events spiegelt der (gecachte) Zustand
// die neuen Events korrekt wider.
func TestStateCacheIncremental(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"sum"}}`)

	writeEvent(t, srv, "/orders/1", "x", `{"amount":100}`)
	first, _ := getState(t, srv, "orders/1") // cache füllen
	if first.State["amount"] != float64(100) || first.EventCount != 1 {
		t.Fatalf("erster stand: amount=%v count=%d, want 100/1", first.State["amount"], first.EventCount)
	}

	writeEvent(t, srv, "/orders/1", "x", `{"amount":50}`)
	writeEvent(t, srv, "/orders/1", "x", `{"amount":25}`)
	second, _ := getState(t, srv, "orders/1") // inkrementell fortgeschrieben
	if second.State["amount"] != float64(175) {
		t.Errorf("nach delta: amount=%v, want 175", second.State["amount"])
	}
	if second.EventCount != 3 || second.Revision != "3" {
		t.Errorf("nach delta: count=%d revision=%q, want 3/3", second.EventCount, second.Revision)
	}
}

// TestStateCacheInvalidatedOnSpecChange: ändert sich die Spec, ändert sich der
// Fingerprint und der Zustand wird neu (mit der neuen Strategie) gefaltet.
func TestStateCacheInvalidatedOnSpecChange(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"sum"}}`)
	writeEvent(t, srv, "/orders/1", "x", `{"amount":10}`)
	writeEvent(t, srv, "/orders/1", "x", `{"amount":40}`)

	sum, _ := getState(t, srv, "orders/1")
	if sum.State["amount"] != float64(50) {
		t.Fatalf("sum = %v, want 50", sum.State["amount"])
	}

	// Strategie auf max umstellen → Cache-Fingerprint passt nicht mehr.
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"max"}}`)
	max, _ := getState(t, srv, "orders/1")
	if max.State["amount"] != float64(40) {
		t.Errorf("nach spec-wechsel: amount=%v, want 40 (max)", max.State["amount"])
	}
}

// TestStateCacheClearedOnDevReset: nach einem Reset (Sequenz startet neu) liefert
// state den frischen Stand, nicht einen stale Cache-Eintrag.
func TestStateCacheClearedOnDevReset(t *testing.T) {
	srv := newDevServer(t)

	writeEvent(t, srv, "/orders/1", "x", `{"status":"old"}`)
	if r, _ := getState(t, srv, "orders/1"); r.State["status"] != "old" {
		t.Fatalf("vor reset: status=%v, want old", r.State["status"])
	}

	if rec := do(t, srv, http.MethodPost, "/api/v1/dev/reset-database", adminToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", rec.Code)
	}

	writeEvent(t, srv, "/orders/1", "x", `{"status":"new"}`)
	r, code := getState(t, srv, "orders/1")
	if code != http.StatusOK {
		t.Fatalf("nach reset status = %d, want 200", code)
	}
	if r.State["status"] != "new" || r.EventCount != 1 {
		t.Errorf("nach reset: status=%v count=%d, want new/1", r.State["status"], r.EventCount)
	}
}

// TestReduceSpecValidation: fehlerhafte Specs ergeben 400.
func TestReduceSpecValidation(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct{ name, body string }{
		{"unbekannte strategie", `{"prefix":"/x","spec":{"fields":{"a":"bogus"}}}`},
		{"leeres segment", `{"prefix":"/x","spec":{"fields":{"a..b":"sum"}}}`},
		{"prefix ohne slash", `{"prefix":"x","spec":{"fields":{"a":"sum"}}}`},
		{"leere spec", `{"prefix":"/x","spec":{}}`},
		{"fehlende spec", `{"prefix":"/x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, srv, http.MethodPost, "/api/v1/register-reduce-spec", adminToken, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestReduceSpecCRUD prüft Registrieren, Lesen (prefix/subject/liste) und Löschen.
func TestReduceSpecCRUD(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"sum"}}`)
	registerSpec(t, srv, "/orders/special", `{"fields":{"amount":"max"}}`)

	// Exakter Prefix.
	rec := do(t, srv, http.MethodGet, "/api/v1/read-reduce-spec?prefix=/orders", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read prefix status = %d, want 200", rec.Code)
	}
	var info struct {
		Prefix string          `json:"prefix"`
		Spec   json.RawMessage `json:"spec"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Prefix != "/orders" {
		t.Errorf("prefix = %q, want /orders", info.Prefix)
	}

	// Wirksame Spec für ein konkretes Subject (längster Prefix).
	rec = do(t, srv, http.MethodGet, "/api/v1/read-reduce-spec?subject=/orders/special/1", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read subject status = %d, want 200", rec.Code)
	}
	info.Prefix = ""
	_ = json.NewDecoder(rec.Body).Decode(&info)
	if info.Prefix != "/orders/special" {
		t.Errorf("effektiver prefix = %q, want /orders/special", info.Prefix)
	}

	// Liste (NDJSON, 2 Einträge).
	rec = do(t, srv, http.MethodGet, "/api/v1/read-reduce-spec", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	lines := 0
	dec := json.NewDecoder(rec.Body)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("ndjson decode: %v", err)
		}
		lines++
	}
	if lines != 2 {
		t.Errorf("liste hat %d einträge, want 2", lines)
	}

	// Löschen + 404 danach.
	rec = do(t, srv, http.MethodDelete, "/api/v1/reduce-spec?prefix=/orders/special", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}
	rec = do(t, srv, http.MethodGet, "/api/v1/read-reduce-spec?prefix=/orders/special", adminToken, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("read nach delete status = %d, want 404", rec.Code)
	}
}

// TestStateCacheConcurrent liest denselben Subject-Zustand parallel, während
// nebenher Events geschrieben werden — deckt Races im LRU/Fold auf (mit -race).
func TestStateCacheConcurrent(t *testing.T) {
	srv := newTestServer(t)
	registerSpec(t, srv, "/orders", `{"fields":{"amount":"sum"}}`)
	writeEvent(t, srv, "/orders/1", "x", `{"amount":1}`)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			writeEvent(t, srv, "/orders/1", "x", `{"amount":1}`)
		}
		close(done)
	}()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				rec := do(t, srv, http.MethodGet, "/api/v1/state/orders/1", adminToken, "")
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want 200", rec.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
	<-done

	final, _ := getState(t, srv, "orders/1")
	if final.State["amount"] != float64(21) {
		t.Errorf("finaler amount = %v, want 21", final.State["amount"])
	}
}

// TestReduceSpecRequiresWriteScope: Registrieren braucht write, nicht nur read.
func TestReduceSpecRequiresWriteScope(t *testing.T) {
	srv := newTestServer(t)
	readTok := seedKey(t, srv.store, "kid_ro01", "rosecretrosecretrosecret00000000", auth.StatusActive, auth.ScopeRead)
	rec := do(t, srv, http.MethodPost, "/api/v1/register-reduce-spec", readTok, `{"prefix":"/x","spec":{"fields":{"a":"sum"}}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status mit read-only = %d, want 403", rec.Code)
	}
}
