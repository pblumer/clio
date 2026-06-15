// Package query stellt die CEL-basierte Prädikat-Auswertung für Events bereit
// (Stufe 4, ADR-017). Ein Prädikat ist ein CEL-Ausdruck über die Variable
// `event` (Metadaten typisiert, `event.data` als dynamische Map), der zu bool
// evaluiert — z. B. `event.type == 'order-placed' && event.data.amount > 100`.
package query

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/pblumer/clio/internal/event"
)

// dataRefRe erkennt, ob ein Ausdruck überhaupt auf `data` Bezug nimmt. Feldzugriff
// auf `event.data` (oder `event["data"]`, `has(event.data…)`) enthält stets das
// Token `data`. Trifft das Muster nicht zu, kann `event.data` nicht referenziert
// werden — dann sparen wir das (teure) Parsen des data-Payloads je Event. Ein
// falsch-positiver Treffer (z. B. der String `'data'`) ist unkritisch: dann wird
// data wie bisher geparst (nur kein Speedup), nie ein falsches Ergebnis.
var dataRefRe = regexp.MustCompile(`\bdata\b`)

// Predicate ist ein kompilierter CEL-Ausdruck, der gegen Events ausgewertet
// werden kann.
type Predicate struct {
	expr     string
	prg      cel.Program
	usesData bool // referenziert der Ausdruck event.data?
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

	p := &Predicate{expr: expr, prg: prg, usesData: dataRefRe.MatchString(expr)}
	c.cache[expr] = p
	return p, nil
}

// Eval wertet das Prädikat gegen ein Event aus. Ein Auswertungsfehler (z. B.
// Zugriff auf ein fehlendes data-Feld) wird zurückgegeben; Aufrufer können ihn
// als „kein Treffer" behandeln. Tipp: mit `has(event.data.x)` defensiv prüfen.
func (p *Predicate) Eval(ev event.Event) (bool, error) {
	m, err := eventToActivation(ev, p.usesData)
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

// Project reduziert ein Event auf die angegebenen Feldpfade. Jeder Pfad ist
// punktsepariert (z. B. "id", "data.title", "data.author.name") und bezieht
// sich auf die JSON-Repräsentation des Events: CloudEvents-Feldnamen auf der
// obersten Ebene ("id", "subject", "type", "data" …) und beliebige
// Verschachtelung innerhalb von "data".
//
// Die Ausgabe bewahrt die Verschachtelung: "data.title" ergibt
// {"data":{"title":...}}. Fehlende Felder werden ausgelassen (kein null).
// Array-Indizierung wird nicht unterstützt; ein Pfad, der durch einen
// Nicht-Map-Wert führt, gilt als fehlend.
func Project(ev event.Event, fields []string) (map[string]any, error) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("event serialisieren: %w", err)
	}
	var src map[string]any
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("event dekodieren: %w", err)
	}
	dst := make(map[string]any)
	for _, f := range fields {
		segs := strings.Split(f, ".")
		if val, ok := lookupPath(src, segs); ok {
			setPath(dst, segs, val)
		}
	}
	return dst, nil
}

// ValidateFields prüft, dass jeder Projektionspfad nicht leer ist und keine
// leeren Segmente enthält (z. B. "data." oder "a..b").
func ValidateFields(fields []string) error {
	for _, f := range fields {
		if f == "" {
			return fmt.Errorf("select-eintrag darf nicht leer sein")
		}
		for _, seg := range strings.Split(f, ".") {
			if seg == "" {
				return fmt.Errorf("select-pfad %q hat ein leeres segment", f)
			}
		}
	}
	return nil
}

// lookupPath folgt einem Segmentpfad in einer verschachtelten Map und liefert
// den gefundenen Wert. Führt ein Segment durch einen Nicht-Map-Wert oder fehlt
// ein Schlüssel, ist der zweite Rückgabewert false.
func lookupPath(src map[string]any, segs []string) (any, bool) {
	var cur any = src
	for _, s := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[s]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// setPath setzt einen Wert unter einem Segmentpfad und legt dabei fehlende
// Zwischen-Maps an.
func setPath(dst map[string]any, segs []string, val any) {
	cur := dst
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = val
			return
		}
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = make(map[string]any)
			cur[s] = next
		}
		cur = next
	}
}

// eventToActivation bildet ein Event auf die CEL-Variable `event` ab.
func eventToActivation(ev event.Event, parseData bool) (map[string]any, error) {
	var data any
	// data nur dekodieren, wenn das Prädikat es auch referenziert — das spart bei
	// type-/subject-Filtern über große Scopes das teuerste Stück pro Event.
	if parseData && len(ev.Data) > 0 {
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
