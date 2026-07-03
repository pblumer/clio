package query

import (
	"strings"
	"testing"
)

// TestEvalCostLimit prüft den CEL-Kostenriegel (WP-4.3/A4): ein pathologischer
// Ausdruck, dessen Auswertung gegen ein einzelnes Event die Laufzeitkosten sprengt
// (hier drei verschachtelte map()-Comprehensions über eine 300-elementige Liste,
// ~27M Operationen), muss bei der Auswertung sauber mit einem Fehler abbrechen statt
// unbegrenzt CPU zu binden.
func TestEvalCostLimit(t *testing.T) {
	c := mustCompiler(t)

	list := "[" + strings.TrimSuffix(strings.Repeat("0,", 300), ",") + "]"
	expr := list + ".map(a, " + list + ".map(b, " + list + ".map(c, c))).size() > 0"

	p, err := c.Compile(expr)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = p.Eval(ev("x", ""))
	if err == nil {
		t.Fatal("erwartete einen Kostenlimit-Fehler, bekam keinen — CEL-CostLimit greift nicht")
	}
	if !strings.Contains(err.Error(), "cost limit") {
		t.Fatalf("unerwarteter Fehler (kein Kostenlimit?): %v", err)
	}
}

// TestEvalNormalExprWithinCost stellt sicher, dass der Kostenriegel legitime,
// realistische Prädikate NICHT fälschlich abbricht.
func TestEvalNormalExprWithinCost(t *testing.T) {
	c := mustCompiler(t)
	p, err := c.Compile("event.type == 'order-placed' && event.data.amount > 100")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := p.Eval(ev("order-placed", `{"amount":150}`))
	if err != nil {
		t.Fatalf("Eval eines normalen Prädikats schlug fehl: %v", err)
	}
	if !got {
		t.Fatal("erwartete Treffer für ein normales Prädikat")
	}
}
