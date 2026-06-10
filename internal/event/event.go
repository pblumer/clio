// Package event definiert die CloudEvents-Domänentypen von cliostore sowie
// die Validierung von Event-Candidates (siehe ADR-004, ADR-005).
package event

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SpecVersion ist die unterstützte CloudEvents-Spezifikationsversion.
const SpecVersion = "1.0"

// Candidate ist ein vom Client gesendeter Event-Vorschlag, bevor er
// gespeichert wurde. Die Felder ID, Time und SpecVersion werden
// serverseitig ergänzt und dürfen vom Client nicht gesetzt werden.
type Candidate struct {
	Source  string          `json:"source"`
	Subject string          `json:"subject"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Event ist ein gespeichertes, unveränderliches Faktum im CloudEvents-Format.
type Event struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Time        string          `json:"time"`
	Source      string          `json:"source"`
	Subject     string          `json:"subject"`
	Type        string          `json:"type"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// Validate prüft, ob ein Candidate die Pflichtfelder korrekt setzt.
// Ein Subject muss mit "/" beginnen (ADR-005).
func (c Candidate) Validate() error {
	if strings.TrimSpace(c.Source) == "" {
		return fmt.Errorf("source ist pflicht")
	}
	if c.Subject == "" || !strings.HasPrefix(c.Subject, "/") {
		return fmt.Errorf("subject muss mit %q beginnen", "/")
	}
	if strings.TrimSpace(c.Type) == "" {
		return fmt.Errorf("type ist pflicht")
	}
	if len(c.Data) > 0 && !json.Valid(c.Data) {
		return fmt.Errorf("data ist kein gültiges JSON")
	}
	return nil
}
