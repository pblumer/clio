package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store öffnen: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestReduceSpecRegisterAndLongestPrefix(t *testing.T) {
	st := openTempStore(t)

	if err := st.RegisterReduceSpec("/orders", json.RawMessage(`{"fields":{"amount":"sum"}}`)); err != nil {
		t.Fatalf("register /orders: %v", err)
	}
	if err := st.RegisterReduceSpec("/orders/special", json.RawMessage(`{"fields":{"amount":"max"}}`)); err != nil {
		t.Fatalf("register /orders/special: %v", err)
	}

	// Längster passender Prefix gewinnt.
	_, prefix, found, err := st.ReduceSpecFor("/orders/special/1")
	if err != nil || !found {
		t.Fatalf("ReduceSpecFor special: found=%v err=%v", found, err)
	}
	if prefix != "/orders/special" {
		t.Errorf("prefix = %q, want /orders/special", prefix)
	}

	// Weniger spezifisches Subject fällt auf /orders zurück.
	_, prefix, found, _ = st.ReduceSpecFor("/orders/normal/1")
	if !found || prefix != "/orders" {
		t.Errorf("prefix = %q (found %v), want /orders", prefix, found)
	}

	// Subject außerhalb jeder Spec → keine Spec.
	_, _, found, _ = st.ReduceSpecFor("/users/1")
	if found {
		t.Errorf("für /users/1 sollte keine spec gelten")
	}
}

func TestReduceSpecOverwriteAndDelete(t *testing.T) {
	st := openTempStore(t)
	if err := st.RegisterReduceSpec("/orders", json.RawMessage(`{"fields":{"amount":"sum"}}`)); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Überschreiben (mutable Konfiguration, kein ErrExists wie bei Schemas).
	if err := st.RegisterReduceSpec("/orders", json.RawMessage(`{"fields":{"amount":"max"}}`)); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	raw, _, found, _ := st.ReduceSpecFor("/orders/1")
	if !found {
		t.Fatalf("nach overwrite nicht gefunden")
	}
	var spec ReduceSpec
	_ = json.Unmarshal(raw, &spec)
	if spec.Fields["amount"] != ReduceMax {
		t.Errorf("amount-strategie = %q, want max", spec.Fields["amount"])
	}

	found, err := st.DeleteReduceSpec("/orders")
	if err != nil || !found {
		t.Fatalf("delete: found=%v err=%v", found, err)
	}
	if _, _, found, _ := st.ReduceSpecFor("/orders/1"); found {
		t.Errorf("nach delete noch gefunden")
	}
	// Erneutes Löschen meldet found=false (kein Fehler).
	if found, _ := st.DeleteReduceSpec("/orders"); found {
		t.Errorf("zweites delete sollte found=false liefern")
	}
}

func TestReduceSpecValidationStore(t *testing.T) {
	st := openTempStore(t)
	bad := []string{
		`{"fields":{"a":"bogus"}}`,
		`{"fields":{"":"sum"}}`,
		`{"fields":{"a..b":"sum"}}`,
		`{}`,
		`{"default":"nope"}`,
	}
	for _, b := range bad {
		if err := st.RegisterReduceSpec("/x", json.RawMessage(b)); !errors.Is(err, ErrReduceSpecValidation) {
			t.Errorf("spec %q: erwartete ErrReduceSpecValidation, bekam %v", b, err)
		}
	}
	// Prefix ohne Slash.
	if err := st.RegisterReduceSpec("x", json.RawMessage(`{"fields":{"a":"sum"}}`)); !errors.Is(err, ErrReduceSpecValidation) {
		t.Errorf("prefix ohne slash: erwartete ErrReduceSpecValidation, bekam %v", err)
	}
}

func TestReduceSpecSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.RegisterReduceSpec("/orders", json.RawMessage(`{"fields":{"amount":"sum"}}`)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = st2.Close() }()
	if _, _, found, _ := st2.ReduceSpecFor("/orders/1"); !found {
		t.Errorf("spec nach reopen nicht gefunden")
	}
}

func TestReduceSpecResetClears(t *testing.T) {
	st := openTempStore(t)
	if err := st.RegisterReduceSpec("/orders", json.RawMessage(`{"fields":{"amount":"sum"}}`)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := st.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	// Reduce-Specs sind abgeleitete Lese-Konfiguration → vom Reset (ADR-022) erfasst.
	if _, _, found, _ := st.ReduceSpecFor("/orders/1"); found {
		t.Errorf("spec sollte nach reset weg sein")
	}
}
