package query

import (
	"encoding/json"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func mustCompiler(t *testing.T) *Compiler {
	t.Helper()
	c, err := NewCompiler()
	if err != nil {
		t.Fatalf("NewCompiler: %v", err)
	}
	return c
}

func ev(typ string, data string) event.Event {
	e := event.Event{ID: "1", Source: "s", Subject: "/orders/1", Type: typ}
	if data != "" {
		e.Data = json.RawMessage(data)
	}
	return e
}

func TestCompileRejectsNonBoolAndInvalid(t *testing.T) {
	c := mustCompiler(t)
	if _, err := c.Compile("event.type"); err == nil {
		t.Fatal("nicht-bool-ausdruck sollte abgelehnt werden")
	}
	if _, err := c.Compile("event.type ==="); err == nil {
		t.Fatal("syntaxfehler sollte abgelehnt werden")
	}
	if _, err := c.Compile("nichtVorhanden > 1"); err == nil {
		t.Fatal("unbekannte variable sollte abgelehnt werden")
	}
}

func TestEvalMetadata(t *testing.T) {
	c := mustCompiler(t)
	p, err := c.Compile(`event.type == 'order-placed' && event.subject.startsWith('/orders')`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	got, err := p.Eval(ev("order-placed", ""))
	if err != nil || !got {
		t.Fatalf("erwartet true, bekam %v / %v", got, err)
	}
	got, err = p.Eval(ev("order-cancelled", ""))
	if err != nil || got {
		t.Fatalf("erwartet false, bekam %v / %v", got, err)
	}
}

func TestEvalDataFields(t *testing.T) {
	c := mustCompiler(t)
	p, err := c.Compile(`has(event.data.amount) && event.data.amount > 100`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if got, _ := p.Eval(ev("t", `{"amount":250}`)); !got {
		t.Fatal("amount 250 sollte matchen")
	}
	if got, _ := p.Eval(ev("t", `{"amount":50}`)); got {
		t.Fatal("amount 50 sollte nicht matchen")
	}
	// has() schützt vor fehlendem Feld -> false, kein Fehler.
	if got, err := p.Eval(ev("t", `{"other":1}`)); err != nil || got {
		t.Fatalf("fehlendes feld: erwartet false/nil, bekam %v/%v", got, err)
	}
}

func TestEvalErrorOnMissingFieldWithoutHas(t *testing.T) {
	c := mustCompiler(t)
	p, _ := c.Compile(`event.data.amount > 100`)
	if _, err := p.Eval(ev("t", `{"other":1}`)); err == nil {
		t.Fatal("zugriff auf fehlendes feld ohne has() sollte einen fehler liefern")
	}
}

func TestCompileCaches(t *testing.T) {
	c := mustCompiler(t)
	p1, _ := c.Compile("event.type == 'x'")
	p2, _ := c.Compile("event.type == 'x'")
	if p1 != p2 {
		t.Fatal("gleicher ausdruck sollte dieselbe Predicate-Instanz liefern (cache)")
	}
}
