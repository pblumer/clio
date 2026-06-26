package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/pblumer/clio/internal/store"
)

// reduceSpec ist die geparste, anwendungsfertige Form einer store.ReduceSpec
// (ADR-041). fieldStrategy bildet einen punkt-separierten Feldpfad auf eine
// Strategie ab; defaultStrategy gilt für nicht genannte Felder (leer = lww).
type reduceSpec struct {
	defaultStrategy string
	fields          map[string]string
}

// parseReduceSpec dekodiert die kanonische Spec-JSON. Ein nil/leeres Ergebnis
// bedeutet „reines LWW-Deep-Merge" (ADR-039-Verhalten).
func parseReduceSpec(raw json.RawMessage) (*reduceSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s store.ReduceSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.Default == "" && len(s.Fields) == 0 {
		return nil, nil
	}
	return &reduceSpec{defaultStrategy: s.Default, fields: s.Fields}, nil
}

// strategyFor liefert die Strategie eines Feldpfads (Default, falls nicht genannt).
func (rs *reduceSpec) strategyFor(path string) string {
	if rs == nil {
		return store.ReduceLWW
	}
	if strat, ok := rs.fields[path]; ok {
		return strat
	}
	if rs.defaultStrategy != "" {
		return rs.defaultStrategy
	}
	return store.ReduceLWW
}

// applyEvent faltet die data-Payload eines Events in den Akkumulator (ADR-041).
// Ohne Spec (rs == nil) ist das exakt das LWW-Deep-Merge aus ADR-039. Mit Spec
// werden zuerst die feldweise nicht-LWW-Strategien angewandt und ihre Pfade aus
// der Payload entfernt; der Rest folgt dem LWW-Deep-Merge.
func applyEvent(acc map[string]any, data json.RawMessage, rs *reduceSpec) {
	if len(data) == 0 {
		return
	}
	var patch map[string]any
	if err := json.Unmarshal(data, &patch); err != nil {
		// Kein JSON-Objekt → für die Feld-Sicht ignoriert (wie ADR-039).
		return
	}
	if rs != nil {
		// Genannte, nicht-LWW-Felder: Strategie anwenden und Pfad aus dem Patch
		// nehmen, damit das Deep-Merge sie nicht überschreibt. Der Default greift
		// implizit über das Deep-Merge des Rests (Default lww) bzw. — bei einem
		// nicht-lww-Default — über applyDefaultStrategy auf jedem verbleibenden Pfad.
		for path, strat := range rs.fields {
			if strat == store.ReduceLWW {
				continue
			}
			segs := strings.Split(path, ".")
			val, ok := lookupPath(patch, segs)
			if !ok {
				continue
			}
			applyStrategy(acc, segs, val, strat)
			deletePath(patch, segs)
		}
		if rs.defaultStrategy != "" && rs.defaultStrategy != store.ReduceLWW {
			applyDefaultStrategy(acc, patch, rs)
			return
		}
	}
	deepMergeInto(acc, patch)
}

// applyDefaultStrategy wendet die Default-Strategie auf jedes Top-Level-Feld des
// (rest-)Patches an, sofern das Feld nicht ohnehin schon eine eigene Strategie
// hatte. Nur für nicht-lww-Defaults relevant.
func applyDefaultStrategy(acc, patch map[string]any, rs *reduceSpec) {
	for k, v := range patch {
		if strat, ok := rs.fields[k]; ok && strat != rs.defaultStrategy {
			// Bereits oben behandelt (eigene Strategie) — überspringen.
			continue
		}
		applyStrategy(acc, []string{k}, v, rs.defaultStrategy)
	}
}

// applyStrategy wendet eine einzelne Strategie auf den Wert val am Pfad segs in
// acc an.
func applyStrategy(acc map[string]any, segs []string, val any, strat string) {
	switch strat {
	case store.ReduceSum:
		f, ok := toFloat(val)
		if !ok {
			return
		}
		cur, _ := toFloat(getPath(acc, segs))
		setPath(acc, segs, cur+f)
	case store.ReduceMin:
		f, ok := toFloat(val)
		if !ok {
			return
		}
		if cur, ok := toFloat(getPath(acc, segs)); ok {
			if f < cur {
				setPath(acc, segs, f)
			}
			return
		}
		setPath(acc, segs, f)
	case store.ReduceMax:
		f, ok := toFloat(val)
		if !ok {
			return
		}
		if cur, ok := toFloat(getPath(acc, segs)); ok {
			if f > cur {
				setPath(acc, segs, f)
			}
			return
		}
		setPath(acc, segs, f)
	case store.ReduceAppend:
		setPath(acc, segs, appendElems(asArray(getPath(acc, segs)), val, false))
	case store.ReduceUnion:
		setPath(acc, segs, appendElems(asArray(getPath(acc, segs)), val, true))
	case store.ReduceFirst:
		if _, present := lookupPath(acc, segs); present {
			return // ersten Wert behalten
		}
		if val == nil {
			return
		}
		setPath(acc, segs, val)
	default: // lww
		if val == nil {
			deletePath(acc, segs)
			return
		}
		setPath(acc, segs, val)
	}
}

// appendElems hängt val an arr an. Ist val ein Array, werden seine Elemente
// einzeln angehängt; sonst val als ein Element. Bei dedup werden nur Elemente
// aufgenommen, die noch nicht (tief-gleich) enthalten sind. null wird ignoriert.
func appendElems(arr []any, val any, dedup bool) []any {
	add := func(e any) {
		if e == nil {
			return
		}
		if dedup {
			for _, ex := range arr {
				if reflect.DeepEqual(ex, e) {
					return
				}
			}
		}
		arr = append(arr, e)
	}
	if sub, ok := val.([]any); ok {
		for _, e := range sub {
			add(e)
		}
	} else {
		add(val)
	}
	return arr
}

// asArray gibt v als []any zurück (leeres Slice, wenn v kein Array ist).
func asArray(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}

// toFloat versucht, v als Zahl zu lesen (JSON-Zahlen sind float64).
func toFloat(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// --- Pfad-Helfer auf verschachtelten map[string]any --------------------------

// lookupPath folgt segs in m; ok=false bei fehlendem Schlüssel oder Nicht-Map.
func lookupPath(m map[string]any, segs []string) (any, bool) {
	var cur any = m
	for _, s := range segs {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := mp[s]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// getPath liefert den Wert an segs oder nil.
func getPath(m map[string]any, segs []string) any {
	v, _ := lookupPath(m, segs)
	return v
}

// setPath setzt val an segs und legt fehlende Zwischen-Maps an. Ein Zwischenwert,
// der keine Map ist, wird durch eine Map ersetzt.
func setPath(m map[string]any, segs []string, val any) {
	cur := m
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = val
			return
		}
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
}

// deletePath entfernt den Schlüssel an segs (no-op, wenn der Pfad nicht existiert).
func deletePath(m map[string]any, segs []string) {
	cur := m
	for i, s := range segs {
		if i == len(segs)-1 {
			delete(cur, s)
			return
		}
		next, ok := cur[s].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// deepMergeInto verschmilzt patch in acc per Last-Write-Wins-Deep-Merge
// (ADR-039): Objekte rekursiv pro Schlüssel; Skalare/Arrays/Typwechsel ersetzen;
// JSON null ist ein Tombstone (löscht den Schlüssel).
func deepMergeInto(acc, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(acc, k)
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			if existing, ok := acc[k].(map[string]any); ok {
				deepMergeInto(existing, sub)
				continue
			}
			nested := map[string]any{}
			deepMergeInto(nested, sub)
			acc[k] = nested
			continue
		}
		acc[k] = v
	}
}

// deepCopyMap erstellt eine tiefe Kopie einer JSON-artigen Map (für Cache-
// Auslieferung, damit der gecachte Stand nicht mit der Antwort geteilt wird).
func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		cp := make([]any, len(t))
		for i, e := range t {
			cp[i] = deepCopyValue(e)
		}
		return cp
	default:
		return t
	}
}
