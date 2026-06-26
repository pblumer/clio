package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// ReduceSpec beschreibt, wie die Zustandssicht eines Subjects gefaltet wird
// (ADR-041): pro Feldpfad (punkt-separiert) eine Strategie, plus eine
// Default-Strategie für nicht aufgeführte Felder. Registriert wird die Spec pro
// **Subject-Prefix**; für ein konkretes Subject gilt die Spec des längsten
// passenden Prefix (Routing-Tabelle).
type ReduceSpec struct {
	// Default ist die Strategie für nicht in Fields genannte Felder. Leer = "lww"
	// (Last-Write-Wins-Deep-Merge, das Verhalten ohne Spec aus ADR-039).
	Default string `json:"default,omitempty"`
	// Fields bildet Feldpfade (z. B. "amount" oder "stats.views") auf eine
	// Strategie ab.
	Fields map[string]string `json:"fields,omitempty"`
}

// ReduceSpecInfo ist ein Listeneintrag: der Prefix samt kanonischer Spec.
type ReduceSpecInfo struct {
	Prefix string          `json:"prefix"`
	Spec   json.RawMessage `json:"spec"`
}

// Erlaubte Reduktionsstrategien (ADR-041). lww ist der Default und entspricht dem
// bisherigen Deep-Merge (ADR-039); die übrigen sind feldweise Akkumulationen.
const (
	ReduceLWW    = "lww"    // Last-Write-Wins (Objekte: Deep-Merge; null = Tombstone)
	ReduceSum    = "sum"    // numerisch aufsummieren
	ReduceMin    = "min"    // numerisches Minimum behalten
	ReduceMax    = "max"    // numerisches Maximum behalten
	ReduceAppend = "append" // an ein Array anhängen (Arrays elementweise)
	ReduceUnion  = "union"  // wie append, aber nur neue (mengenartig)
	ReduceFirst  = "first"  // ersten nicht-null-Wert behalten (spätere ignorieren)
)

// validReduceStrategies ist die Menge der erlaubten Strategie-Namen.
var validReduceStrategies = map[string]struct{}{
	ReduceLWW: {}, ReduceSum: {}, ReduceMin: {}, ReduceMax: {},
	ReduceAppend: {}, ReduceUnion: {}, ReduceFirst: {},
}

// ErrReduceSpecValidation wird bei einer fehlerhaften Reduce-Spec zurückgegeben
// (unbekannte Strategie, leerer/ungültiger Feldpfad, ungültiges Prefix/JSON).
var ErrReduceSpecValidation = errors.New("reduce-spec-validierung fehlgeschlagen")

// validateReduceSpec prüft Strategie-Namen und Feldpfade.
func validateReduceSpec(raw json.RawMessage) (ReduceSpec, error) {
	var spec ReduceSpec
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return spec, fmt.Errorf("%w: ungültiges JSON: %v", ErrReduceSpecValidation, err)
	}
	if spec.Default != "" {
		if _, ok := validReduceStrategies[spec.Default]; !ok {
			return spec, fmt.Errorf("%w: unbekannte default-strategie %q", ErrReduceSpecValidation, spec.Default)
		}
	}
	if len(spec.Fields) == 0 && spec.Default == "" {
		return spec, fmt.Errorf("%w: leere spec (weder fields noch default)", ErrReduceSpecValidation)
	}
	for path, strat := range spec.Fields {
		if path == "" {
			return spec, fmt.Errorf("%w: leerer feldpfad", ErrReduceSpecValidation)
		}
		for _, seg := range strings.Split(path, ".") {
			if seg == "" {
				return spec, fmt.Errorf("%w: feldpfad %q hat ein leeres segment", ErrReduceSpecValidation, path)
			}
		}
		if _, ok := validReduceStrategies[strat]; !ok {
			return spec, fmt.Errorf("%w: unbekannte strategie %q für feld %q", ErrReduceSpecValidation, strat, path)
		}
	}
	return spec, nil
}

// RegisterReduceSpec registriert (oder überschreibt) die Reduce-Spec für einen
// Subject-Prefix. Anders als Event-Schemas (ADR-014) ist eine Reduce-Spec
// **mutable Lese-Konfiguration** (kein historisches Faktum) — sie darf überschrieben
// und gelöscht werden; das ändert nur abgeleitete Sichten, nie gespeicherte Events.
func (s *Store) RegisterReduceSpec(prefix string, raw json.RawMessage) error {
	if prefix == "" || prefix[0] != '/' {
		return fmt.Errorf("%w: prefix muss mit \"/\" beginnen", ErrReduceSpecValidation)
	}
	if _, err := validateReduceSpec(raw); err != nil {
		return err
	}
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return fmt.Errorf("%w: spec kompaktieren: %v", ErrReduceSpecValidation, err)
	}
	return s.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketReduceSpecs).Put([]byte(prefix), canonical)
	})
}

// ReduceSpecFor liefert die wirksame Reduce-Spec für ein Subject: die Spec des
// **längsten** registrierten Prefix, der das Subject abdeckt. `prefix` ist der
// gewählte Prefix (Fingerprint-Bestandteil); found=false bedeutet „keine Spec →
// Default-LWW" (ADR-039-Verhalten).
func (s *Store) ReduceSpecFor(subject string) (raw json.RawMessage, prefix string, found bool, err error) {
	err = s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketReduceSpecs).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			p := string(k)
			if !MatchSubject(subject, p, true) {
				continue
			}
			if !found || len(p) > len(prefix) {
				prefix = p
				raw = append(json.RawMessage(nil), v...)
				found = true
			}
		}
		return nil
	})
	return raw, prefix, found, err
}

// DeleteReduceSpec entfernt die Spec eines Prefix. found=false, wenn keine existierte.
func (s *Store) DeleteReduceSpec(prefix string) (found bool, err error) {
	err = s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketReduceSpecs)
		if b.Get([]byte(prefix)) == nil {
			return nil
		}
		found = true
		return b.Delete([]byte(prefix))
	})
	return found, err
}

// ReduceSpecs listet alle registrierten Specs, alphabetisch nach Prefix.
func (s *Store) ReduceSpecs() ([]ReduceSpecInfo, error) {
	var out []ReduceSpecInfo
	err := s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketReduceSpecs).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			out = append(out, ReduceSpecInfo{
				Prefix: string(k),
				Spec:   append(json.RawMessage(nil), v...),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out, nil
}
