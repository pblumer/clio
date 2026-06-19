package store

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/clio/internal/auth"
	"github.com/pblumer/clio/internal/event"
)

func TestAppendAndListAudit(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"), Options{})

	for i := 0; i < 5; i++ {
		if err := s.AppendAudit(AuditEntry{Action: "key.create", ActorKID: "kid_a", Target: "kid_x"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	n, err := s.CountAudit()
	if err != nil || n != 5 {
		t.Fatalf("count = %d err=%v, want 5", n, err)
	}

	// Neueste zuerst, Seq monoton fallend, Result defaultet auf success.
	entries, err := s.AuditEntries(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("len = %d, want 5", len(entries))
	}
	if entries[0].Seq != 5 || entries[4].Seq != 1 {
		t.Fatalf("reihenfolge falsch: %d..%d", entries[0].Seq, entries[4].Seq)
	}
	for _, e := range entries {
		if e.Result != AuditSuccess || e.Time.IsZero() {
			t.Fatalf("eintrag unvollständig: %+v", e)
		}
	}

	// limit
	top2, err := s.AuditEntries(2, 0)
	if err != nil || len(top2) != 2 || top2[0].Seq != 5 || top2[1].Seq != 4 {
		t.Fatalf("limit-2 falsch: %+v err=%v", top2, err)
	}
	// before-Cursor: nur Seq < 3 -> 2, 1
	before3, err := s.AuditEntries(0, 3)
	if err != nil || len(before3) != 2 || before3[0].Seq != 2 || before3[1].Seq != 1 {
		t.Fatalf("before-3 falsch: %+v err=%v", before3, err)
	}
}

func TestAppendAuditFailure(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"), Options{})
	if err := s.AppendAudit(AuditEntry{Action: "key.revoke", Result: AuditFailure, Error: "unbekannter kid", Target: "kid_x"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.AuditEntries(0, 0)
	if len(entries) != 1 || entries[0].Result != AuditFailure || entries[0].Error == "" {
		t.Fatalf("failure-eintrag falsch: %+v", entries)
	}
}

// TestAuditSurvivesReset: der Dev-Reset (ADR-022) löscht Events, aber NICHT das
// Audit-Log (ADR-032) — die Spur des Resets bleibt erhalten.
func TestAuditSurvivesReset(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"), Options{})

	// Ein Event und ein Audit-Eintrag.
	if _, err := s.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAudit(AuditEntry{Action: "dev.reset"}); err != nil {
		t.Fatal(err)
	}
	// Auch der Schlüsselbund bleibt (Gegenprobe-Invariante von ADR-025).
	if err := s.PutKey(auth.Key{KID: "kid_keep", Status: auth.StatusActive}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Reset(); err != nil {
		t.Fatal(err)
	}

	if n, _ := s.Count(); n != 0 {
		t.Fatalf("events nach reset = %d, want 0", n)
	}
	if n, _ := s.CountAudit(); n != 1 {
		t.Fatalf("audit nach reset = %d, want 1 (erhalten)", n)
	}
	if _, ok, _ := s.GetKey("kid_keep"); !ok {
		t.Fatal("key nach reset verloren")
	}
}
