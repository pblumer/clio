package partition

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

// genKeys erzeugt deterministisch eine Menge aggregat-ähnlicher Keys (kein
// Zufall, damit der Test reproduzierbar ist).
func genKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("/orders/%d/customer/%d", i, (i*2654435761)%100003)
	}
	return keys
}

func TestKeyFromSource(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/orders/42", "/orders/42"},
		{"  /orders/42  ", "/orders/42"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		if got := KeyFromSource(tc.in); got != tc.want {
			t.Errorf("KeyFromSource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewRingValidation(t *testing.T) {
	if _, err := NewRing(0, 100); err == nil {
		t.Error("NewRing(0, …) sollte einen Fehler liefern")
	}
	if _, err := NewRing(4, 0); err == nil {
		t.Error("NewRing(…, 0) sollte einen Fehler liefern")
	}
	if _, err := NewRing(1, 1); err != nil {
		t.Errorf("NewRing(1,1) unerwarteter Fehler: %v", err)
	}
}

// TestDeterministischUeberNeuaufbau: derselbe Key liefert dieselbe Partition,
// auch wenn der Ring frisch aufgebaut wird (simuliert Prozess-Neustart).
func TestDeterministischUeberNeuaufbau(t *testing.T) {
	r1, _ := NewRing(8, 128)
	r2, _ := NewRing(8, 128)
	for _, k := range genKeys(2000) {
		if a, b := r1.Partition(k), r2.Partition(k); a != b {
			t.Fatalf("nicht deterministisch: Partition(%q) = %d vs %d", k, a, b)
		}
		// Auch innerhalb desselben Rings stabil bei wiederholtem Aufruf.
		if a, b := r1.Partition(k), r1.Partition(k); a != b {
			t.Fatalf("instabil bei Wiederholung: %q -> %d, dann %d", k, a, b)
		}
	}
}

// TestPartitionImBereich: jede zugewiesene Partition liegt in 0..N-1.
func TestPartitionImBereich(t *testing.T) {
	const n = 16
	r, _ := NewRing(n, 64)
	for _, k := range genKeys(5000) {
		if id := r.Partition(k); int(id) < 0 || int(id) >= n {
			t.Fatalf("Partition(%q) = %d außerhalb 0..%d", k, id, n-1)
		}
	}
}

// TestEinzelpartitionImmerNull: der Default N=1 ist verhaltensgleich zur
// nicht-partitionierten Single-Instance (ARCHITECTURE.md §4.1) — alle Keys
// landen in Partition 0.
func TestEinzelpartitionImmerNull(t *testing.T) {
	r, _ := NewRing(1, 128)
	for _, k := range append(genKeys(1000), "", "  ", "/x") {
		if id := r.Partition(k); id != 0 {
			t.Fatalf("N=1: Partition(%q) = %d, want 0", k, id)
		}
	}
	if id := r.PartitionForSource("  /orders/7  "); id != 0 {
		t.Fatalf("N=1: PartitionForSource = %d, want 0", id)
	}
}

// TestVerteilungAnnaeherndGleich: über viele Keys verteilt der Ring die Last
// grob gleichmäßig. Toleranz bewusst weit (±35 % vom Mittel), weil konsistentes
// Hashing mit endlich vielen virtuellen Knoten nie perfekt gleichverteilt.
func TestVerteilungAnnaeherndGleich(t *testing.T) {
	const n, keys = 8, 40000
	r, _ := NewRing(n, 256)
	counts := make([]int, n)
	for _, k := range genKeys(keys) {
		counts[r.Partition(k)]++
	}
	mean := float64(keys) / float64(n)
	for id, c := range counts {
		ratio := float64(c) / mean
		if ratio < 0.65 || ratio > 1.35 {
			t.Errorf("Partition %d hat %d Keys (Ratio %.2f), außerhalb [0.65, 1.35]", id, c, ratio)
		}
	}
}

// TestMinimalMigration: Wächst der Ring von N auf N+1 Partitionen, wandert nur
// ein kleiner Bruchteil der Keys — die Kerneigenschaft des konsistenten
// Hashings (ADR-038). Zum Vergleich: naives Modulo-Mapping würde die große
// Mehrheit verschieben. Der Test belegt beides.
func TestMinimalMigration(t *testing.T) {
	const n, keys = 8, 40000
	old, _ := NewRing(n, 256)
	grown, _ := NewRing(n+1, 256)
	ks := genKeys(keys)

	moves := Rebalance(old, grown, ks)
	frac := float64(len(moves)) / float64(keys)

	// Theoretisch ~1/(N+1) ≈ 11 %. Großzügige Obergrenze gegen Flakiness.
	if frac == 0 || frac > 0.22 {
		t.Errorf("konsistentes Hashing: %.1f%% der Keys gewandert, erwartet ~%.1f%% (Grenze 22%%)",
			frac*100, 100.0/float64(n+1))
	}

	// Gegenprobe: naives Modulo verschiebt drastisch mehr.
	moduloMoved := 0
	for _, k := range ks {
		if moduloPartition(k, n) != moduloPartition(k, n+1) {
			moduloMoved++
		}
	}
	if moduloMoved <= len(moves) {
		t.Errorf("Erwartung verletzt: Modulo bewegt %d Keys, konsistentes Hashing nur %d — "+
			"konsistentes Hashing sollte deutlich weniger bewegen", moduloMoved, len(moves))
	}
}

// TestRebalanceLeerBeiGleichemRing: ein Rebalance auf einen identischen Ring
// bewegt nichts.
func TestRebalanceLeerBeiGleichemRing(t *testing.T) {
	r, _ := NewRing(8, 128)
	if moves := Rebalance(r, r, genKeys(1000)); len(moves) != 0 {
		t.Errorf("Rebalance auf identischen Ring bewegte %d Keys, want 0", len(moves))
	}
}

// moduloPartition ist die naive Vergleichsabbildung (NICHT in Produktion
// verwendet) für TestMinimalMigration.
func moduloPartition(key string, n int) ID {
	sum := sha256.Sum256([]byte(key))
	return ID(binary.BigEndian.Uint32(sum[:4]) % uint32(n))
}
