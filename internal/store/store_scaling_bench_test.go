package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/event"
)

// TestScalingValidation ist die On-Demand-Validierung des Storage-Scaling-Plans
// (Etappe 4) durch die ECHTE Store-API (Append inkl. Hash-Kette und Indizes) —
// im Gegensatz zum Roh-bbolt-Spike der Etappe 0. Sie misst, ob das vorab
// dimensionierte InitialMmapSize die Schreib-Latenzspitzen beim Wachsen unter
// Leselast beseitigt.
//
// Läuft NICHT in der normalen CI (zu lang). Aktivieren:
//
//	CLIO_SCALING_BENCH=1 go test ./internal/store/ -run TestScalingValidation -v -timeout 30m
//
// Steuerbar: CLIO_SCALING_EVENTS (Default 1_500_000), CLIO_SCALING_INITIAL_MB
// (Default 2048 für den presize-Lauf).
func TestScalingValidation(t *testing.T) {
	if os.Getenv("CLIO_SCALING_BENCH") == "" {
		t.Skip("setze CLIO_SCALING_BENCH=1 für die Skalierungs-Validierung")
	}
	events := envInt("CLIO_SCALING_EVENTS", 1_500_000)
	initialMB := envInt("CLIO_SCALING_INITIAL_MB", 2048)

	t.Run("baseline", func(t *testing.T) {
		runScalingPhase(t, events, 0)
	})
	t.Run("presize", func(t *testing.T) {
		runScalingPhase(t, events, initialMB)
	})
}

func runScalingPhase(t *testing.T, totalEvents, initialMB int) {
	t.Helper()
	opts := Options{SyncMode: SyncOff} // fsync aus -> isoliert den Remap-Effekt
	if initialMB > 0 {
		opts.InitialMmapSize = initialMB << 20
	}
	st, err := OpenWithOptions(t.TempDir()+"/scaling.db", opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Leser erzeugen mmaplock-Kontention (Remaps müssen auf sie warten): jeder
	// hält wiederholt kurz eine Lese-Transaktion über einen begrenzten Scan.
	var stop atomic.Bool
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				n := 0
				_ = st.ReadFunc("/", true, ReadOptions{}, func(event.Event) bool {
					n++
					return n < 2000 // früh abbrechen: kurze, häufige Read-Tx
				})
			}
		}()
	}

	const batch = 500
	data := json.RawMessage(`{"payload":"` + strconv.Itoa(initialMB) + `-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`)
	cands := make([]event.Candidate, batch)
	for i := range cands {
		cands[i] = event.Candidate{Source: "bench", Subject: "/v/" + strconv.Itoa(i%500), Type: "t", Data: data}
	}

	const phase = 250_000
	var lat []float64
	phaseStart := time.Now()
	done := 0
	lastMB := int64(-1)

	t.Logf("--- mode initialMB=%d ---", initialMB)
	t.Logf("%-10s %-12s %-10s %-10s %-10s %-8s", "events", "thrpt/s", "p50_ms", "p99_ms", "max_ms", "file_MB")
	for done < totalEvents {
		s := time.Now()
		if _, err := st.Append(cands, nil); err != nil {
			t.Fatalf("append: %v", err)
		}
		lat = append(lat, float64(time.Since(s).Microseconds())/1000)
		done += batch

		if done%phase == 0 {
			el := time.Since(phaseStart).Seconds()
			fi, _ := os.Stat(st.path())
			mb := fi.Size() >> 20
			grew := ""
			if lastMB >= 0 && mb != lastMB {
				grew = fmt.Sprintf(" (+%dMB)", mb-lastMB)
			}
			lastMB = mb
			t.Logf("%-10d %-12.0f %-10.2f %-10.2f %-10.2f %-8d%s",
				done, float64(phase)/el, pctl(lat, 50), pctl(lat, 99), pctl(lat, 100), mb, grew)
			lat = lat[:0]
			phaseStart = time.Now()
		}
	}
	stop.Store(true)
	readers.Wait()
}

func pctl(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if p >= 100 {
		return s[len(s)-1]
	}
	i := p * len(s) / 100
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
