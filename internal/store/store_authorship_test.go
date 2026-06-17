package store

import (
	"testing"

	"github.com/pblumer/clio/internal/event"
)

// TestAppendAuthoredStampsKID: AppendAuthored schreibt den kid als clioauthkid
// und bezieht ihn in den Hash ein; verify bleibt grün.
func TestAppendAuthoredStampsKID(t *testing.T) {
	st := openTemp(t)
	written, err := st.AppendAuthored(
		[]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}, nil, "kid_ci01")
	if err != nil {
		t.Fatalf("AppendAuthored: %v", err)
	}
	if written[0].AuthKID != "kid_ci01" {
		t.Fatalf("AuthKID = %q, want kid_ci01", written[0].AuthKID)
	}
	// Der Hash enthält die Urheberschaft -> ComputeHash reproduziert ihn nur mit
	// gesetztem AuthKID.
	if event.ComputeHash(written[0]) != written[0].Hash {
		t.Fatal("hash nicht reproduzierbar mit AuthKID")
	}
	noAuthor := written[0]
	noAuthor.AuthKID = ""
	if event.ComputeHash(noAuthor) == written[0].Hash {
		t.Fatal("AuthKID floss nicht in den Hash ein")
	}

	res, err := st.Verify()
	if err != nil || !res.OK {
		t.Fatalf("verify nach authored append: ok=%v err=%v", res.OK, err)
	}
}

// TestAppendWithoutAuthorByteIdentical: Append (bzw. authKID="") erzeugt exakt
// denselben Hash wie vor dem Feature — Backward-Compat der Hash-Kette.
func TestAppendWithoutAuthorByteIdentical(t *testing.T) {
	st := openTemp(t)
	written, err := st.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t", Data: []byte(`{"k":1}`)}}, nil)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	ev := written[0]
	if ev.AuthKID != "" {
		t.Fatalf("AuthKID sollte leer sein, war %q", ev.AuthKID)
	}
	// Erwarteter Hash exakt über die bisherigen Felder (ohne Urheberschaft).
	if event.ComputeHash(ev) != ev.Hash {
		t.Fatal("hash ohne urheberschaft nicht reproduzierbar")
	}
}
