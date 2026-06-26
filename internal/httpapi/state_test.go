package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// writeEvent schreibt ein einzelnes Event über die echte HTTP-Route und schlägt
// fehl, wenn der Store es nicht annimmt. data ist roher JSON (oder "" für keins).
func writeEvent(t *testing.T, srv *Server, subject, typ, data string) {
	t.Helper()
	ev := `{"source":"s","subject":"` + subject + `","type":"` + typ + `"`
	if data != "" {
		ev += `,"data":` + data
	}
	ev += `}`
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, `{"events":[`+ev+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("write-events (%s/%s) status = %d, want 200; body=%s", subject, typ, rec.Code, rec.Body.String())
	}
}

// getState ruft GET /api/v1/state/<path> auf und dekodiert die Antwort.
func getState(t *testing.T, srv *Server, path string) (*stateResponse, int) {
	t.Helper()
	rec := do(t, srv, http.MethodGet, "/api/v1/state/"+path, adminToken, "")
	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}
	var resp stateResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("state-antwort dekodieren: %v", err)
	}
	return &resp, rec.Code
}

// TestStateLastWriteWinsMerge prüft den Kern: data-Payloads werden in
// Schreibreihenfolge per Last-Write-Wins-Deep-Merge zum aktuellen Zustand gefaltet.
func TestStateLastWriteWinsMerge(t *testing.T) {
	srv := newTestServer(t)

	writeEvent(t, srv, "/orders/1", "created", `{"status":"new","amount":100,"customer":{"id":7,"name":"Ada"}}`)
	writeEvent(t, srv, "/orders/1", "amountChanged", `{"amount":250}`)
	writeEvent(t, srv, "/orders/1", "shipped", `{"status":"shipped","customer":{"name":"Ada Lovelace"}}`)

	resp, code := getState(t, srv, "orders/1")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if resp.Subject != "/orders/1" {
		t.Errorf("subject = %q, want /orders/1", resp.Subject)
	}
	if resp.EventCount != 3 {
		t.Errorf("eventCount = %d, want 3", resp.EventCount)
	}
	if resp.Revision != "3" || resp.LastEventID != "3" {
		t.Errorf("revision/lastEventId = %q/%q, want 3/3", resp.Revision, resp.LastEventID)
	}
	if resp.FirstEventID != "1" {
		t.Errorf("firstEventId = %q, want 1", resp.FirstEventID)
	}
	if resp.LastEventType != "shipped" {
		t.Errorf("lastEventType = %q, want shipped", resp.LastEventType)
	}

	// Skalar: amount durch das jüngste Event ersetzt; status durch shipped.
	if got := resp.State["amount"]; got != float64(250) {
		t.Errorf("state.amount = %v, want 250", got)
	}
	if got := resp.State["status"]; got != "shipped" {
		t.Errorf("state.status = %v, want shipped", got)
	}
	// Objekt: customer.id bleibt (aus dem ersten Event), name vom letzten gemerged.
	cust, ok := resp.State["customer"].(map[string]any)
	if !ok {
		t.Fatalf("state.customer kein objekt: %v", resp.State["customer"])
	}
	if cust["id"] != float64(7) {
		t.Errorf("customer.id = %v, want 7", cust["id"])
	}
	if cust["name"] != "Ada Lovelace" {
		t.Errorf("customer.name = %v, want Ada Lovelace", cust["name"])
	}
}

// TestStateTombstone prüft, dass JSON null ein Feld entfernt (Tombstone-Konvention).
func TestStateTombstone(t *testing.T) {
	srv := newTestServer(t)

	writeEvent(t, srv, "/users/u1", "set", `{"email":"a@b.c","phone":"123"}`)
	writeEvent(t, srv, "/users/u1", "unset", `{"phone":null}`)

	resp, _ := getState(t, srv, "users/u1")
	if _, present := resp.State["phone"]; present {
		t.Errorf("phone sollte durch null-tombstone entfernt sein: %v", resp.State["phone"])
	}
	if resp.State["email"] != "a@b.c" {
		t.Errorf("email = %v, want a@b.c", resp.State["email"])
	}
}

// TestStateNotFound: ein Subject ohne Events hat keinen Zustand → 404.
func TestStateNotFound(t *testing.T) {
	srv := newTestServer(t)
	if _, code := getState(t, srv, "does/not/exist"); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

// TestStateAt rekonstruiert den historischen Stand „as of" einer Event-ID.
func TestStateAt(t *testing.T) {
	srv := newTestServer(t)
	writeEvent(t, srv, "/orders/1", "created", `{"status":"new"}`)
	writeEvent(t, srv, "/orders/1", "paid", `{"status":"paid"}`)
	writeEvent(t, srv, "/orders/1", "shipped", `{"status":"shipped"}`)

	rec := do(t, srv, http.MethodGet, "/api/v1/state/orders/1?at=2", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp stateResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("dekodieren: %v", err)
	}
	if resp.State["status"] != "paid" {
		t.Errorf("state.status @2 = %v, want paid", resp.State["status"])
	}
	if resp.EventCount != 2 || resp.Revision != "2" {
		t.Errorf("eventCount/revision = %d/%q, want 2/2", resp.EventCount, resp.Revision)
	}
	if resp.At != "2" {
		t.Errorf("at = %q, want 2", resp.At)
	}
}

// TestStateTypeFilter faltet nur Events der angegebenen Typen.
func TestStateTypeFilter(t *testing.T) {
	srv := newTestServer(t)
	writeEvent(t, srv, "/orders/1", "created", `{"status":"new"}`)
	writeEvent(t, srv, "/orders/1", "note", `{"status":"ignored","note":"hi"}`)
	writeEvent(t, srv, "/orders/1", "shipped", `{"status":"shipped"}`)

	rec := do(t, srv, http.MethodGet, "/api/v1/state/orders/1?type=created&type=shipped", adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp stateResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("dekodieren: %v", err)
	}
	if resp.EventCount != 2 {
		t.Errorf("eventCount = %d, want 2 (note gefiltert)", resp.EventCount)
	}
	if resp.State["status"] != "shipped" {
		t.Errorf("state.status = %v, want shipped", resp.State["status"])
	}
	if _, present := resp.State["note"]; present {
		t.Errorf("note sollte herausgefiltert sein: %v", resp.State["note"])
	}
}

// TestStateNotRecursive: state ist single-subject; Events auf Kind-Subjects
// gehören nicht zum Zustand des Eltern-Subjects.
func TestStateNotRecursive(t *testing.T) {
	srv := newTestServer(t)
	writeEvent(t, srv, "/orders", "x", `{"a":1}`)
	writeEvent(t, srv, "/orders/1", "y", `{"b":2}`)

	resp, _ := getState(t, srv, "orders")
	if resp.EventCount != 1 {
		t.Errorf("eventCount = %d, want 1 (nur /orders, nicht /orders/1)", resp.EventCount)
	}
	if _, present := resp.State["b"]; present {
		t.Errorf("kind-feld b darf nicht im eltern-zustand sein")
	}
}

// TestStateRequiresAuth: ohne read-scope kein Zustand.
func TestStateRequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	writeEvent(t, srv, "/orders/1", "created", `{"status":"new"}`)
	if rec := do(t, srv, http.MethodGet, "/api/v1/state/orders/1", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status ohne auth = %d, want 401", rec.Code)
	}
}

// TestStateNonObjectData: Events mit nicht-Objekt-data (Array/Skalar) zählen,
// tragen aber nichts zur Feld-Sicht bei.
func TestStateNonObjectData(t *testing.T) {
	srv := newTestServer(t)
	writeEvent(t, srv, "/x/1", "a", `{"k":"v"}`)
	writeEvent(t, srv, "/x/1", "b", `[1,2,3]`)
	writeEvent(t, srv, "/x/1", "c", `"scalar"`)

	resp, _ := getState(t, srv, "x/1")
	if resp.EventCount != 3 {
		t.Errorf("eventCount = %d, want 3", resp.EventCount)
	}
	if len(resp.State) != 1 || resp.State["k"] != "v" {
		t.Errorf("state = %v, want {k:v}", resp.State)
	}
}
