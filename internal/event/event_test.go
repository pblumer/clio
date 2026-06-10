package event

import (
	"encoding/json"
	"testing"
)

func TestCandidateValidate(t *testing.T) {
	valid := Candidate{Source: "s", Subject: "/a", Type: "t"}

	tests := []struct {
		name    string
		c       Candidate
		wantErr bool
	}{
		{"gültig ohne data", valid, false},
		{"gültig mit data", Candidate{Source: "s", Subject: "/a", Type: "t", Data: json.RawMessage(`{"x":1}`)}, false},
		{"gültig subject root", Candidate{Source: "s", Subject: "/", Type: "t"}, false},
		{"source leer", Candidate{Source: "", Subject: "/a", Type: "t"}, true},
		{"source nur whitespace", Candidate{Source: "  ", Subject: "/a", Type: "t"}, true},
		{"subject leer", Candidate{Source: "s", Subject: "", Type: "t"}, true},
		{"subject ohne slash", Candidate{Source: "s", Subject: "a", Type: "t"}, true},
		{"type leer", Candidate{Source: "s", Subject: "/a", Type: ""}, true},
		{"type nur whitespace", Candidate{Source: "s", Subject: "/a", Type: "\t"}, true},
		{"data ungültiges json", Candidate{Source: "s", Subject: "/a", Type: "t", Data: json.RawMessage(`{nope`)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// TestEventJSONRoundTrip stellt die CloudEvents-Feldnamen sicher.
func TestEventJSONRoundTrip(t *testing.T) {
	ev := Event{
		SpecVersion: SpecVersion,
		ID:          "7",
		Time:        "2026-06-10T00:00:00Z",
		Source:      "lib",
		Subject:     "/books/42",
		Type:        "acquired",
		Data:        json.RawMessage(`{"title":"Dune"}`),
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	for _, key := range []string{"specversion", "id", "time", "source", "subject", "type", "data"} {
		if _, ok := asMap[key]; !ok {
			t.Errorf("feld %q fehlt im JSON", key)
		}
	}

	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if back.ID != ev.ID || back.Subject != ev.Subject || string(back.Data) != string(ev.Data) {
		t.Fatalf("round-trip weicht ab: %+v", back)
	}
}

// TestDataOmittedWhenEmpty: leeres data erscheint nicht im JSON (omitempty).
func TestDataOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Event{SpecVersion: SpecVersion, ID: "1", Subject: "/a", Source: "s", Type: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := asMap["data"]; ok {
		t.Errorf("data sollte bei leerem wert weggelassen werden")
	}
}
