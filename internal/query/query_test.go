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

func TestEvalErrorOnInvalidData(t *testing.T) {
	c := mustCompiler(t)
	p, _ := c.Compile(`has(event.data.x)`)
	// Ungültiges JSON in data -> eventToActivation scheitert -> Eval-Fehler.
	if _, err := p.Eval(ev("t", `{kaputt`)); err == nil {
		t.Fatal("ungültiges data-JSON sollte einen Eval-Fehler liefern")
	}
}

func TestPredicateExpr(t *testing.T) {
	c := mustCompiler(t)
	const src = "event.type == 'x'"
	p, err := c.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if p.Expr() != src {
		t.Fatalf("Expr() = %q, want %q", p.Expr(), src)
	}
}

func TestValidateFields(t *testing.T) {
	tests := []struct {
		name    string
		fields  []string
		wantErr bool
	}{
		{"leer", nil, false},
		{"gültige pfade", []string{"id", "data.title", "data.a.b"}, false},
		{"leerer eintrag", []string{"id", ""}, true},
		{"führender punkt", []string{".x"}, true},
		{"nachgestellter punkt", []string{"data."}, true},
		{"doppelter punkt", []string{"a..b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFields(tt.fields)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFields(%v) err=%v, wantErr=%v", tt.fields, err, tt.wantErr)
			}
		})
	}
}

func TestProject(t *testing.T) {
	e := event.Event{
		ID:      "7",
		Source:  "s",
		Subject: "/orders/7",
		Type:    "placed",
		Data:    json.RawMessage(`{"amount":250,"customer":{"name":"Ada","vip":true}}`),
	}

	t.Run("top-level und verschachtelt", func(t *testing.T) {
		got, err := Project(e, []string{"id", "data.amount", "data.customer.name"})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if got["id"] != "7" {
			t.Fatalf("id falsch: %+v", got)
		}
		data := got["data"].(map[string]any)
		if data["amount"].(float64) != 250 {
			t.Fatalf("data.amount falsch: %+v", data)
		}
		cust := data["customer"].(map[string]any)
		if cust["name"] != "Ada" {
			t.Fatalf("data.customer.name falsch: %+v", cust)
		}
		if _, ok := cust["vip"]; ok {
			t.Fatalf("nicht selektiertes vip erscheint: %+v", cust)
		}
		if _, ok := got["type"]; ok {
			t.Fatalf("nicht selektiertes type erscheint: %+v", got)
		}
	})

	t.Run("fehlende felder werden ausgelassen", func(t *testing.T) {
		got, err := Project(e, []string{"id", "data.fehlt", "nichtVorhanden"})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if got["id"] != "7" {
			t.Fatalf("id falsch: %+v", got)
		}
		if _, ok := got["data"]; ok {
			t.Fatalf("fehlendes data.fehlt sollte keine data-map anlegen: %+v", got)
		}
		if _, ok := got["nichtVorhanden"]; ok {
			t.Fatalf("fehlendes top-level-feld sollte fehlen: %+v", got)
		}
	})

	t.Run("pfad durch nicht-map gilt als fehlend", func(t *testing.T) {
		// data.amount ist eine Zahl; data.amount.x kann nicht navigiert werden.
		got, err := Project(e, []string{"data.amount.x"})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("erwartet leeres ergebnis, bekam %+v", got)
		}
	})
}

func TestCompileCaches(t *testing.T) {
	c := mustCompiler(t)
	p1, _ := c.Compile("event.type == 'x'")
	p2, _ := c.Compile("event.type == 'x'")
	if p1 != p2 {
		t.Fatal("gleicher ausdruck sollte dieselbe Predicate-Instanz liefern (cache)")
	}
}
