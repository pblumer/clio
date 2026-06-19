package activity

import (
	"sync"
	"testing"
	"time"
)

const testWindow = time.Minute

func TestRecord_FirstActivityStartsSessionAndCounts(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	started := r.Record("kid_a", "alice", []string{"read"}, CategoryRead, true, t0)
	if !started {
		t.Fatal("erste Aktivität soll eine Session starten (sessionStarted=true)")
	}

	snaps := r.Snapshot(t0)
	if len(snaps) != 1 {
		t.Fatalf("erwarte 1 Eintrag, habe %d", len(snaps))
	}
	s := snaps[0]
	if s.Reads != 1 || s.Writes != 0 || s.AdminOps != 0 {
		t.Errorf("read-Aktivität falsch gezählt: %+v", s)
	}
	if !s.Online {
		t.Error("Schlüssel soll direkt nach Aktivität online sein")
	}
	if !s.FirstSeen.Equal(t0) || !s.LastSeen.Equal(t0) {
		t.Errorf("FirstSeen/LastSeen falsch: %+v", s)
	}
	if !s.SessionStarted.Equal(t0) {
		t.Errorf("SessionStarted soll t0 sein: %v", s.SessionStarted)
	}
}

func TestRecord_WithinWindowNoNewSession(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	if !r.Record("kid_a", "alice", []string{"read"}, CategoryRead, true, t0) {
		t.Fatal("erste Aktivität soll Session starten")
	}
	// Innerhalb des Fensters → keine neue Session.
	if r.Record("kid_a", "alice", []string{"read"}, CategoryRead, true, t0.Add(30*time.Second)) {
		t.Error("Folge-Aktivität im Fenster soll KEINE neue Session melden")
	}
	if got := r.Snapshot(t0.Add(30 * time.Second))[0].Reads; got != 2 {
		t.Errorf("erwarte Reads=2, habe %d", got)
	}
}

func TestRecord_AfterWindowStartsNewSession(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	r.Record("kid_a", "alice", []string{"write"}, CategoryWrite, true, t0)

	// Nach Ablauf des Fensters → neue Session.
	later := t0.Add(2 * time.Minute)
	if !r.Record("kid_a", "alice", []string{"write"}, CategoryWrite, true, later) {
		t.Error("Aktivität nach Fensterablauf soll eine neue Session melden")
	}
	s := r.Snapshot(later)[0]
	if !s.SessionStarted.Equal(later) {
		t.Errorf("SessionStarted soll auf %v zurückgesetzt sein, ist %v", later, s.SessionStarted)
	}
	if s.Writes != 2 {
		t.Errorf("erwarte Writes=2, habe %d", s.Writes)
	}
}

func TestRecord_DeniedCountsButNoSessionNoOnline(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	if r.Record("kid_b", "bob", []string{"read"}, CategoryRead, false, t0) {
		t.Error("abgelehnte Anfrage soll keine Session starten")
	}
	s := r.Snapshot(t0)[0]
	if s.Denied != 1 {
		t.Errorf("erwarte Denied=1, habe %d", s.Denied)
	}
	if s.Online {
		t.Error("rein abgelehnter Zugriff soll nicht online machen")
	}
	if !s.LastSeen.IsZero() {
		t.Errorf("LastSeen soll bei nur-abgelehnt leer sein, ist %v", s.LastSeen)
	}
}

func TestObserve_KeepsOnlineWithoutRecentActivity(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	if !r.OpenObserve("kid_c", "carol", []string{"read"}, t0) {
		t.Fatal("erste offene Observe-Verbindung soll Session starten")
	}
	// Weit nach dem Fenster, aber Verbindung ist offen → weiterhin online.
	far := t0.Add(10 * time.Minute)
	s := r.Snapshot(far)[0]
	if !s.Online || s.OpenObserves != 1 {
		t.Errorf("offene Observe-Verbindung soll online halten: %+v", s)
	}

	r.CloseObserve("kid_c", far)
	if got := r.Snapshot(far)[0].OpenObserves; got != 0 {
		t.Errorf("nach Close soll OpenObserves=0 sein, ist %d", got)
	}
	// Direkt nach dem Schließen (lastSeen=far) noch im Fenster → online.
	if !r.Snapshot(far)[0].Online {
		t.Error("unmittelbar nach Close soll der Schlüssel noch im Fenster online sein")
	}
	// Ein Fenster später → offline.
	if r.Snapshot(far.Add(2 * time.Minute))[0].Online {
		t.Error("ein Fenster nach Close soll der Schlüssel offline sein")
	}
}

func TestCloseObserve_UnknownKidIsNoop(t *testing.T) {
	r := New(testWindow)
	// Darf nicht paniken oder Einträge anlegen.
	r.CloseObserve("kid_unknown", time.Now())
	if len(r.Snapshot(time.Now())) != 0 {
		t.Error("CloseObserve auf unbekanntem kid soll keinen Eintrag anlegen")
	}
}

func TestSweep_EndsExpiredSessionsOnly(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	r.Record("kid_active", "a", []string{"read"}, CategoryRead, true, t0)
	r.Record("kid_expired", "e", []string{"read"}, CategoryRead, true, t0)
	r.OpenObserve("kid_observer", "o", []string{"read"}, t0)

	// kid_active bleibt aktiv (jüngste Aktivität), die anderen nicht.
	sweepAt := t0.Add(90 * time.Second)
	r.Record("kid_active", "a", []string{"read"}, CategoryRead, true, sweepAt)

	ended := r.Sweep(sweepAt)
	if len(ended) != 1 {
		t.Fatalf("erwarte genau 1 beendete Session, habe %d: %+v", len(ended), ended)
	}
	if ended[0].KID != "kid_expired" {
		t.Errorf("erwarte kid_expired als beendet, habe %s", ended[0].KID)
	}
	if !ended[0].SessionStarted.Equal(t0) {
		t.Errorf("SessionStarted der beendeten Session soll t0 sein, ist %v", ended[0].SessionStarted)
	}

	// Zweiter Sweep meldet dieselbe Session nicht erneut (Markierung geleert).
	if again := r.Sweep(sweepAt); len(again) != 0 {
		t.Errorf("zweiter Sweep soll nichts melden, hat %+v", again)
	}

	// Observer mit offener Verbindung bleibt online und wird nie ge-sweept —
	// auch lange nach dem Fenster (andere, inaktive Sessions dürfen hier enden).
	far := sweepAt.Add(time.Hour)
	for _, e := range r.Sweep(far) {
		if e.KID == "kid_observer" {
			t.Errorf("offener Observer darf nie beendet werden, wurde aber ge-sweept: %+v", e)
		}
	}
	if !snapshotFor(r, "kid_observer", far).Online {
		t.Error("Observer mit offener Verbindung soll online bleiben")
	}
}

// snapshotFor sucht den Snapshot eines bestimmten kid (Sortierung variiert).
func snapshotFor(r *Registry, kid string, now time.Time) Snapshot {
	for _, s := range r.Snapshot(now) {
		if s.KID == kid {
			return s
		}
	}
	return Snapshot{}
}

func TestSnapshot_OnlineFirstOrdering(t *testing.T) {
	r := New(testWindow)
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	r.Record("kid_old", "old", []string{"read"}, CategoryRead, true, t0)
	r.Record("kid_new", "new", []string{"read"}, CategoryRead, true, t0.Add(30*time.Second))

	// Betrachtungszeitpunkt: kid_old außerhalb (80s ≥ Fenster), kid_new innerhalb
	// (50s < Fenster) des 60s-Fensters.
	now := t0.Add(80 * time.Second)
	snaps := r.Snapshot(now)
	if len(snaps) != 2 {
		t.Fatalf("erwarte 2 Einträge, habe %d", len(snaps))
	}
	if snaps[0].KID != "kid_new" || !snaps[0].Online {
		t.Errorf("online-Eintrag soll zuerst stehen, habe %+v", snaps[0])
	}
	if snaps[1].KID != "kid_old" || snaps[1].Online {
		t.Errorf("offline-Eintrag soll zuletzt stehen, habe %+v", snaps[1])
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := New(testWindow)
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			kid := "kid_" + string(rune('a'+n%5))
			for j := 0; j < 100; j++ {
				r.Record(kid, "n", []string{"read"}, CategoryRead, true, now)
				r.OpenObserve(kid, "n", []string{"read"}, now)
				r.CloseObserve(kid, now)
				_ = r.Snapshot(now)
				_ = r.Sweep(now)
			}
		}(i)
	}
	wg.Wait()
	if len(r.Snapshot(now)) != 5 {
		t.Errorf("erwarte 5 distinkte kids, habe %d", len(r.Snapshot(now)))
	}
}
