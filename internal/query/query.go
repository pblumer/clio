// Package query stellt die CEL-basierte Prädikat-Auswertung für Events bereit
// (Stufe 4, ADR-017). Ein Prädikat ist ein CEL-Ausdruck über die Variable
// `event` (Metadaten typisiert, `event.data` als dynamische Map), der zu bool
// evaluiert — z. B. `event.type == 'order-placed' && event.data.amount > 100`.
package query

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/pblumer/clio/internal/event"
)

// Predicate ist ein kompilierter CEL-Ausdruck, der gegen Events ausgewertet
// werden kann.
type Predicate struct {
	expr string
	prg  cel.Program
}

// Expr liefert den ursprünglichen Ausdruck (für Logging/Fehlermeldungen).
func (p *Predicate) Expr() string { return p.expr }

// Compiler kompiliert CEL-Prädikate gegen eine feste Umgebung und cacht das
// Ergebnis je Ausdruck. Nebenläufig sicher.
type Compiler struct {
	env   *cel.Env
	mu    sync.Mutex
	cache map[string]*Predicate
}

// NewCompiler erstellt einen Compiler mit der Event-Umgebung.
func NewCompiler() (*Compiler, error) {
	env, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("CEL-umgebung: %w", err)
	}
	return &Compiler{env: env, cache: make(map[string]*Predicate)}, nil
}

// Compile übersetzt einen Ausdruck in ein Prädikat. Der Ausdruck muss zu bool
// evaluieren. Ergebnisse werden je Ausdruck gecacht.
func (c *Compiler) Compile(expr string) (*Predicate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.cache[expr]; ok {
		return p, nil
	}

	ast, iss := c.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("ungültiger ausdruck: %w", iss.Err())
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("ausdruck muss bool ergeben, ergibt %s", ast.OutputType())
	}
	prg, err := c.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("programm erzeugen: %w", err)
	}

	p := &Predicate{expr: expr, prg: prg}
	c.cache[expr] = p
	return p, nil
}

// Eval wertet das Prädikat gegen ein Event aus. Ein Auswertungsfehler (z. B.
// Zugriff auf ein fehlendes data-Feld) wird zurückgegeben; Aufrufer können ihn
// als „kein Treffer" behandeln. Tipp: mit `has(event.data.x)` defensiv prüfen.
func (p *Predicate) Eval(ev event.Event) (bool, error) {
	m, err := eventToActivation(ev)
	if err != nil {
		return false, err
	}
	out, _, err := p.prg.Eval(m)
	if err != nil {
		return false, fmt.Errorf("auswertung: %w", err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("ergebnis ist kein bool")
	}
	return b, nil
}

// eventToActivation bildet ein Event auf die CEL-Variable `event` ab.
func eventToActivation(ev event.Event) (map[string]any, error) {
	var data any
	if len(ev.Data) > 0 {
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return nil, fmt.Errorf("data dekodieren: %w", err)
		}
	}
	return map[string]any{
		"event": map[string]any{
			"id":      ev.ID,
			"time":    ev.Time,
			"source":  ev.Source,
			"subject": ev.Subject,
			"type":    ev.Type,
			"data":    data,
		},
	}, nil
}
