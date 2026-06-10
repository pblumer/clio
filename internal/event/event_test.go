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

func TestComputeHash(t *testing.T) {
	base := Event{
		SpecVersion: SpecVersion, ID: "1", Time: "2026-06-10T00:00:00Z",
		Source: "lib", Subject: "/books/42", Type: "acquired",
		DataContentType: JSONContentType, Data: json.RawMessage(`{"k":1}`),
		PredecessorHash: GenesisHash,
	}

	h := ComputeHash(base)
	if len(h) != 64 {
		t.Fatalf("hash-länge = %d, want 64 (hex sha256)", len(h))
	}
	if h != ComputeHash(base) {
		t.Fatal("ComputeHash ist nicht deterministisch")
	}

	// Hash und Signature dürfen NICHT eingehen.
	withMeta := base
	withMeta.Hash = "egal"
	sig := "sig"
	withMeta.Signature = &sig
	if ComputeHash(withMeta) != h {
		t.Fatal("hash/signature dürfen den Hash nicht beeinflussen")
	}

	// Jede inhaltliche Änderung muss den Hash ändern.
	for name, mut := range map[string]func(*Event){
		"type":            func(e *Event) { e.Type = "borrowed" },
		"data":            func(e *Event) { e.Data = json.RawMessage(`{"k":2}`) },
		"subject":         func(e *Event) { e.Subject = "/books/43" },
		"predecessorhash": func(e *Event) { e.PredecessorHash = "ff" },
	} {
		e := base
		mut(&e)
		if ComputeHash(e) == h {
			t.Fatalf("Änderung an %q ließ den Hash unverändert", name)
		}
	}
}

// TestComputeHashFieldBoundaries: Längenpräfixe verhindern, dass sich
// Feldgrenzen verschieben lassen (z. B. ("ab","c") vs ("a","bc")).
func TestComputeHashFieldBoundaries(t *testing.T) {
	a := Event{Source: "ab", Subject: "c", PredecessorHash: GenesisHash}
	b := Event{Source: "a", Subject: "bc", PredecessorHash: GenesisHash}
	if ComputeHash(a) == ComputeHash(b) {
		t.Fatal("Feldgrenzen-Kollision: ('ab','c') und ('a','bc') ergeben denselben Hash")
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
