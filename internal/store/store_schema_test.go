package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

const amountSchema = `{"type":"object","required":["amount"],"properties":{"amount":{"type":"number"}},"additionalProperties":false}`

func TestRegisterSchemaAndValidateWrites(t *testing.T) {
	st := openTemp(t)
	if err := st.RegisterSchema("order", json.RawMessage(amountSchema)); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Gültiges Event.
	if _, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/o/1", Type: "order", Data: json.RawMessage(`{"amount":42}`)},
	}, nil); err != nil {
		t.Fatalf("gültiger write: %v", err)
	}

	// Ungültiges Event -> ErrSchemaValidation, nichts geschrieben.
	_, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/o/2", Type: "order", Data: json.RawMessage(`{"foo":1}`)},
	}, nil)
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("ungültiger write: erwartete ErrSchemaValidation, bekam %v", err)
	}
	if c, _ := st.Count(); c != 1 {
		t.Fatalf("nach abgelehntem write: count=%d, want 1", c)
	}

	// Typ ohne Schema bleibt unbeschränkt.
	if _, err := st.Append([]event.Candidate{
		{Source: "s", Subject: "/x", Type: "frei", Data: json.RawMessage(`{"beliebig":true}`)},
	}, nil); err != nil {
		t.Fatalf("schemaloser typ: %v", err)
	}
}

func TestRegisterSchemaTwiceConflict(t *testing.T) {
	st := openTemp(t)
	if err := st.RegisterSchema("order", json.RawMessage(amountSchema)); err != nil {
		t.Fatalf("erste registrierung: %v", err)
	}
	if err := st.RegisterSchema("order", json.RawMessage(`{"type":"object"}`)); !errors.Is(err, ErrSchemaExists) {
		t.Fatalf("zweite registrierung: erwartete ErrSchemaExists, bekam %v", err)
	}
}

func TestRegisterInvalidSchema(t *testing.T) {
	st := openTemp(t)
	err := st.RegisterSchema("order", json.RawMessage(`{"type":"nonsense-type"}`))
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("ungültiges schema: erwartete ErrSchemaValidation, bekam %v", err)
	}
}

func TestRegisterSchemaAgainstViolatingHistory(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/p", Type: "plain", Data: json.RawMessage(`{"x":1}`)})

	err := st.RegisterSchema("plain", json.RawMessage(`{"type":"object","required":["y"]}`))
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("verletzende historie: erwartete ErrSchemaValidation, bekam %v", err)
	}
	// Schema darf NICHT gespeichert worden sein.
	if _, found, _ := st.SchemaFor("plain"); found {
		t.Fatal("schema wurde trotz Verletzung gespeichert")
	}
}

func TestSchemaForAndHasSchema(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/o", Type: "order", Data: json.RawMessage(`{"amount":1}`)})

	if _, found, _ := st.SchemaFor("order"); found {
		t.Fatal("vor registrierung sollte kein schema gefunden werden")
	}
	if err := st.RegisterSchema("order", json.RawMessage(amountSchema)); err != nil {
		t.Fatalf("register: %v", err)
	}
	raw, found, err := st.SchemaFor("order")
	if err != nil || !found || len(raw) == 0 {
		t.Fatalf("SchemaFor: %v found=%v len=%d", err, found, len(raw))
	}

	types, _ := st.EventTypes()
	if len(types) != 1 || types[0].Type != "order" || !types[0].HasSchema {
		t.Fatalf("EventTypes hasSchema falsch: %+v", types)
	}
}

func TestSchemaPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.RegisterSchema("order", json.RawMessage(amountSchema)); err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	// Nach Reopen muss die Validierung weiter greifen.
	_, err = st2.Append([]event.Candidate{
		{Source: "s", Subject: "/o", Type: "order", Data: json.RawMessage(`{"foo":1}`)},
	}, nil)
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("nach reopen: erwartete ErrSchemaValidation, bekam %v", err)
	}
}
