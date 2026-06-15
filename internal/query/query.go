// Package query stellt die CEL-basierte Prädikat-Auswertung für Events bereit
// (Stufe 4, ADR-017). Ein Prädikat ist ein CEL-Ausdruck über die Variable
// `event` (Metadaten typisiert, `event.data` als dynamische Map), der zu bool
// evaluiert — z. B. `event.type == 'order-placed' && event.data.amount > 100`.
package query

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"

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
	expr        string
	prg         cel.Program
	usesData    bool     // referenziert der Ausdruck event.data?
	reqTypes    []string // geforderte event.type-Werte (sortiert), falls einschränkbar
	typeBounded bool     // ist die Menge der erlaubten Typen sicher bestimmbar?
}

// Expr liefert den ursprünglichen Ausdruck (für Logging/Fehlermeldungen).
func (p *Predicate) Expr() string { return p.expr }

// RequiredTypes liefert die Menge der event.type-Werte, auf die das Prädikat
// notwendigerweise eingeschränkt ist (z. B. {„order-placed"} für
// `event.type == 'order-placed' && …`). Das zweite Resultat ist false, wenn sich
// keine sichere Einschränkung ableiten lässt — dann muss der gesamte Scope
// gescannt werden. Eine leere Menge mit true bedeutet: kein Typ kann das
// Prädikat erfüllen (Ergebnis ist leer). Ermöglicht den Typ-Index in run-query.
func (p *Predicate) RequiredTypes() ([]string, bool) {
	if !p.typeBounded {
		return nil, false
	}
	return p.reqTypes, true
}

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
	if set, ok := requiredTypes(ast.NativeRep().Expr()); ok {
		p.reqTypes = sortedKeys(set)
		p.typeBounded = true
	}
	c.cache[expr] = p
	return p, nil
}

// requiredTypes leitet aus dem CEL-AST die Menge der event.type-Werte ab, auf die
// der Ausdruck NOTWENDIG eingeschränkt ist (ok=true). ok=false bedeutet: keine
// sichere Einschränkung (möglicher Treffer bei beliebigem Typ) → Full-Scan.
// Konservativ: alles Unbekannte ergibt ok=false, nie eine zu enge Menge.
func requiredTypes(e celast.Expr) (map[string]struct{}, bool) {
	if e.Kind() != celast.CallKind {
		return nil, false
	}
	call := e.AsCall()
	args := call.Args()
	switch call.FunctionName() {
	case operators.Equals:
		if len(args) == 2 {
			if t, ok := typeEqString(args[0], args[1]); ok {
				return map[string]struct{}{t: {}}, true
			}
			if t, ok := typeEqString(args[1], args[0]); ok {
				return map[string]struct{}{t: {}}, true
			}
		}
	case operators.In:
		if len(args) == 2 && isEventType(args[0]) {
			if set, ok := stringList(args[1]); ok {
				return set, true
			}
		}
	case operators.LogicalAnd:
		// UND: der Typ muss beide Einschränkungen erfüllen → Schnittmenge; eine
		// unbekannte Seite schränkt nicht ein (= „alle Typen").
		sa, oka := requiredTypes(args[0])
		sb, okb := requiredTypes(args[1])
		switch {
		case oka && okb:
			return intersect(sa, sb), true
		case oka:
			return sa, true
		case okb:
			return sb, true
		}
	case operators.LogicalOr:
		// ODER: nur einschränkbar, wenn BEIDE Seiten den Typ einschränken → Vereinigung.
		sa, oka := requiredTypes(args[0])
		sb, okb := requiredTypes(args[1])
		if oka && okb {
			return union(sa, sb), true
		}
	}
	return nil, false
}

// isEventType prüft, ob ein Ausdruck der Feldzugriff `event.type` ist.
func isEventType(e celast.Expr) bool {
	if e.Kind() != celast.SelectKind {
		return false
	}
	sel := e.AsSelect()
	if sel.FieldName() != "type" {
		return false
	}
	op := sel.Operand()
	return op.Kind() == celast.IdentKind && op.AsIdent() == "event"
}

// typeEqString liefert den String, falls a == `event.type` und b ein String-Literal ist.
func typeEqString(a, b celast.Expr) (string, bool) {
	if !isEventType(a) {
		return "", false
	}
	return stringLiteral(b)
}

func stringLiteral(e celast.Expr) (string, bool) {
	if e.Kind() != celast.LiteralKind {
		return "", false
	}
	s, ok := e.AsLiteral().Value().(string)
	return s, ok
}

// stringList liefert die Menge, falls e ein Listenliteral aus lauter Strings ist.
func stringList(e celast.Expr) (map[string]struct{}, bool) {
	if e.Kind() != celast.ListKind {
		return nil, false
	}
	set := make(map[string]struct{})
	for _, el := range e.AsList().Elements() {
		s, ok := stringLiteral(el)
		if !ok {
			return nil, false
		}
		set[s] = struct{}{}
	}
	return set, true
}

func intersect(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func union(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
