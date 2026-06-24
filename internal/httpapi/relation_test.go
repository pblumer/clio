package httpapi

import (
	"net/http"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// TestIdentityRelation schreibt employee- und mailbox-Aggregate und stellt
// die Relation mailbox.attached → employee.assigned mit atomaren Writes her.
// Anschliessend werden run-query-Abfragen verwendet, um die Bidirektionalitaet
// der Relation nachzuweisen.
func TestIdentityRelation(t *testing.T) {
	srv := newTestServer(t)

	// 1. Aggregate anlegen (3 Employees + 3 Mailboxes).
	writeBody := `{"events":[
		{"source":"identity","subject":"/employees/E-000001","type":"employee.created","data":{"firstName":"Max","lastName":"Muster"}},
		{"source":"identity","subject":"/employees/E-000002","type":"employee.created","data":{"firstName":"Erika","lastName":"Mustermann"}},
		{"source":"identity","subject":"/employees/E-000003","type":"employee.created","data":{"firstName":"Hans","lastName":"Beispiel"}},
		{"source":"identity","subject":"/mailboxes/MBX-000001","type":"mailbox.created","data":{"email":"max.muster@example.com","quotaMb":5120}},
		{"source":"identity","subject":"/mailboxes/MBX-000002","type":"mailbox.created","data":{"email":"erika.mustermann@example.com","quotaMb":2048}},
		{"source":"identity","subject":"/mailboxes/MBX-000003","type":"mailbox.created","data":{"email":"hans.beispiel@example.com","quotaMb":1024}}
	]}`
	rec := do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, writeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("create aggregates = %d, want %d", rec.Code, http.StatusOK)
	}

	// 2. Relation atomar herstellen (mailbox.attached + employee.assigned).
	attachBody := `{"events":[
		{"source":"identity","subject":"/employees/E-000001","type":"mailbox.attached","data":{"mailboxId":"MBX-000001","email":"max.muster@example.com"}},
		{"source":"identity","subject":"/employees/E-000002","type":"mailbox.attached","data":{"mailboxId":"MBX-000002","email":"erika.mustermann@example.com"}},
		{"source":"identity","subject":"/mailboxes/MBX-000001","type":"employee.assigned","data":{"employeeId":"E-000001"}},
		{"source":"identity","subject":"/mailboxes/MBX-000002","type":"employee.assigned","data":{"employeeId":"E-000002"}}
	]}`
	rec = do(t, srv, http.MethodPost, "/api/v1/write-events", adminToken, attachBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach relation = %d, want %d", rec.Code, http.StatusOK)
	}

	// 3. CEL-Query: alle mailbox.attached Events unter /employees/.
	q1 := `{"subject":"/employees/","recursive":true,"where":"event.type == 'mailbox.attached'"}`
	rec = do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken, q1)
	if rec.Code != http.StatusOK {
		t.Fatalf("query mailbox.attached = %d, want %d", rec.Code, http.StatusOK)
	}
	var r1 []event.Event
	for _, ev := range decodeNDJSON(t, rec.Body.String()) {
		r1 = append(r1, ev)
	}
	if len(r1) != 2 {
		t.Fatalf("mailbox.attached events = %d, want 2", len(r1))
	}
	for _, ev := range r1 {
		if ev.Type != "mailbox.attached" {
			t.Fatalf("unexpected type %q, want mailbox.attached", ev.Type)
		}
	}

	// 4. CEL-Query: alle employee.assigned Events unter /mailboxes/.
	q2 := `{"subject":"/mailboxes/","recursive":true,"where":"event.type == 'employee.assigned'"}`
	rec = do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken, q2)
	if rec.Code != http.StatusOK {
		t.Fatalf("query employee.assigned = %d, want %d", rec.Code, http.StatusOK)
	}
	var r2 []event.Event
	for _, ev := range decodeNDJSON(t, rec.Body.String()) {
		r2 = append(r2, ev)
	}
	if len(r2) != 2 {
		t.Fatalf("employee.assigned events = %d, want 2", len(r2))
	}
	for _, ev := range r2 {
		if ev.Type != "employee.assigned" {
			t.Fatalf("unexpected type %q, want employee.assigned", ev.Type)
		}
	}

	// 5. E-000003 hat KEIN mailbox.attached — Negativ-Test.
	q3 := `{"subject":"/employees/E-000003","recursive":false,"where":"event.type == 'mailbox.attached'"}`
	rec = do(t, srv, http.MethodPost, "/api/v1/run-query", adminToken, q3)
	if rec.Code != http.StatusOK {
		t.Fatalf("query negative = %d, want %d", rec.Code, http.StatusOK)
	}
	var r3 []event.Event
	for _, ev := range decodeNDJSON(t, rec.Body.String()) {
		r3 = append(r3, ev)
	}
	if len(r3) != 0 {
		t.Fatalf("negative events = %d, want 0", len(r3))
	}
}
