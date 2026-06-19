package main

import "testing"

func TestOrderIDFromSubject(t *testing.T) {
	cases := map[string]string{
		"/orders/o-42":      "o-42",
		"/orders/abc":       "abc",
		"/orders":           "",
		"/orders/":          "",
		"/orders/a/b":       "", // nur direkte Order-Subjects
		"/customers/c-1":    "",
		"/orders/o-1/items": "",
	}
	for subject, want := range cases {
		if got := orderIDFromSubject(subject); got != want {
			t.Errorf("orderIDFromSubject(%q) = %q, want %q", subject, got, want)
		}
	}
}

func TestParseID(t *testing.T) {
	if id, err := parseID("42"); err != nil || id != 42 {
		t.Fatalf("parseID(42) = %d, %v", id, err)
	}
	if _, err := parseID("nope"); err == nil {
		t.Fatal("parseID(nope) sollte fehlschlagen")
	}
}

func TestRedactDSN(t *testing.T) {
	in := "postgres://clio:secret@127.0.0.1:5432/db?sslmode=disable"
	got := redactDSN(in)
	if got == in || containsSecret(got) {
		t.Fatalf("redactDSN hat das Passwort nicht entfernt: %q", got)
	}
}

func containsSecret(s string) bool {
	for i := 0; i+6 <= len(s); i++ {
		if s[i:i+6] == "secret" {
			return true
		}
	}
	return false
}
