package store

import (
	"testing"

	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/query"
)

func compilePred(t *testing.T, expr string) *query.Predicate {
	t.Helper()
	c, err := query.NewCompiler()
	if err != nil {
		t.Fatalf("compiler: %v", err)
	}
	p, err := c.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	return p
}

func TestQueryPreconditionEmpty(t *testing.T) {
	st := openTemp(t)
	pred := compilePred(t, "event.type == 'opened'")
	pre := []Precondition{{Type: PreconditionQueryResultEmpty, Subject: "/accounts/42", Predicate: pred}}

	// Leerer Scope -> Bedingung erfüllt, Write geht durch.
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/accounts/42", Type: "opened"}}, pre); err != nil {
		t.Fatalf("erster write: %v", err)
	}
	// Jetzt existiert ein 'opened' -> Bedingung verletzt -> 409-äquivalent.
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/accounts/42", Type: "opened"}}, pre); !errorsIsPrecondition(err) {
		t.Fatalf("zweiter write: erwartete ErrPreconditionFailed, bekam %v", err)
	}
}

func TestQueryPreconditionNonEmpty(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/accounts/42", Type: "opened"})
	pred := compilePred(t, "event.type == 'opened'")

	// 'opened' existiert -> NonEmpty erfüllt.
	nonEmpty := []Precondition{{Type: PreconditionQueryResultNonEmpty, Subject: "/accounts/42", Predicate: pred}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/accounts/42", Type: "closed"}}, nonEmpty); err != nil {
		t.Fatalf("nonEmpty erfüllt: %v", err)
	}
	// Leerer Scope -> NonEmpty verletzt.
	onEmpty := []Precondition{{Type: PreconditionQueryResultNonEmpty, Subject: "/accounts/99", Predicate: pred}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/accounts/99", Type: "closed"}}, onEmpty); !errorsIsPrecondition(err) {
		t.Fatalf("nonEmpty auf leerem scope: erwartete ErrPreconditionFailed, bekam %v", err)
	}
}

func TestQueryPreconditionRecursiveAndNilPredicate(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/orders/1", Type: "placed"})

	// Rekursiv + Prädikat: existiert ein placed unter /orders? -> Empty verletzt.
	pred := compilePred(t, "event.type == 'placed'")
	rec := []Precondition{{Type: PreconditionQueryResultEmpty, Subject: "/orders", Recursive: true, Predicate: pred}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/orders/2", Type: "placed"}}, rec); !errorsIsPrecondition(err) {
		t.Fatalf("rekursiv: erwartete ErrPreconditionFailed, bekam %v", err)
	}

	// nil-Prädikat = reine Scope-Existenz: /orders/1 ist nicht leer -> Empty verletzt.
	nilPred := []Precondition{{Type: PreconditionQueryResultEmpty, Subject: "/orders/1", Predicate: nil}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/orders/1", Type: "x"}}, nilPred); !errorsIsPrecondition(err) {
		t.Fatalf("nil-prädikat: erwartete ErrPreconditionFailed, bekam %v", err)
	}
}
