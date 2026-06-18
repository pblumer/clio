package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

func TestRemapWarning(t *testing.T) {
	const initial = 1000

	cases := []struct {
		name      string
		data      int64
		initial   int64
		threshold int
		wantPct   float64
		wantWarn  bool
	}{
		{"keine grenze", 900, 0, 80, 0, false},
		{"weit unter schwelle", 500, initial, 80, 50, false},
		{"genau auf schwelle", 800, initial, 80, 80, true},
		{"über schwelle", 950, initial, 80, 95, true},
		{"knapp unter schwelle", 799, initial, 80, 79.9, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pct, warn := remapWarning(tc.data, tc.initial, tc.threshold)
			if warn != tc.wantWarn {
				t.Errorf("warn = %v, want %v", warn, tc.wantWarn)
			}
			if tc.initial > 0 && (pct < tc.wantPct-0.05 || pct > tc.wantPct+0.05) {
				t.Errorf("pct = %v, want ~%v", pct, tc.wantPct)
			}
		})
	}
}

// TestStartBackgroundMaintenanceNoop stellt sicher, dass der Monitor ohne
// konfigurierte Grenze nicht startet (kein Panic, kehrt sofort zurück).
func TestStartBackgroundMaintenanceNoop(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DBInitialMB == 0 -> keine Grenze -> Monitor startet nicht.
	startBackgroundMaintenance(ctx, st, config.Config{DBInitialMB: 0, DBMonitorInterval: time.Second}, logger)
	// DBMonitorInterval == 0 -> aus.
	startBackgroundMaintenance(ctx, st, config.Config{DBInitialMB: 64, DBMonitorInterval: 0}, logger)
	// Compaction aus -> Scheduler startet nicht.
	startBackgroundMaintenance(ctx, st, config.Config{DBCompactEnabled: false}, logger)
	// Compaction an (langes Intervall) -> Scheduler startet, läuft aber nicht an;
	// ctx-Cancel beendet ihn. Verifiziert die Verdrahtung ohne Panik.
	startBackgroundMaintenance(ctx, st, config.Config{DBCompactEnabled: true, DBCompactIntervalH: 6}, logger)
	cancel()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

// TestHeadroomTickHysterese prüft die Warn-/Entwarn-Logik eines einzelnen Ticks
// inklusive Hysterese: erst über der Schwelle warnen, dann erst deutlich darunter
// wieder entwarnen — und dazwischen den Zustand halten.
func TestHeadroomTickHysterese(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger := discardLogger()
	cfg := config.Config{DBGrowThresholdPct: 80}

	// Winzige Grenze (1 Byte) -> realer Daten-Füllstand liegt weit darüber -> warn.
	if got := headroomTick(st, 1, cfg, logger, false); got != true {
		t.Fatalf("über Schwelle: warned = %v, want true", got)
	}
	// Bereits gewarnt und immer noch über Schwelle -> Zustand bleibt true
	// (keine erneute Warnung, kein Entwarnen).
	if got := headroomTick(st, 1, cfg, logger, true); got != true {
		t.Fatalf("weiter über Schwelle: warned = %v, want true (halten)", got)
	}
	// Riesige Grenze -> Füllstand ~0 % -> deutlich unter (Schwelle-5) -> entwarnen.
	if got := headroomTick(st, 1<<60, cfg, logger, true); got != false {
		t.Fatalf("deutlich unter Schwelle: warned = %v, want false (entwarnt)", got)
	}
}

// TestHeadroomTickStatsError stellt sicher, dass ein Statistik-Fehler den
// warned-Zustand unverändert lässt (nur Log) — kein falsches Entwarnen bei einem
// transienten Fehler. Der geschlossene Store erzwingt den Fehler.
func TestHeadroomTickStatsError(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = st.Close() // ab jetzt schlägt Stats() fehl

	cfg := config.Config{DBGrowThresholdPct: 80}
	if got := headroomTick(st, 1, cfg, discardLogger(), true); got != true {
		t.Fatalf("warned bei Stats-Fehler = %v, want true (unverändert)", got)
	}
}

// TestRunCompaction deckt den Erfolgs- und den Fehlerpfad der Online-
// Kompaktierung ab: ein offener Store kompaktiert ohne Fehler; ein geschlossener
// Store lässt CompactInPlace fehlschlagen (nur Log, kein Panik).
func TestRunCompaction(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	logger := discardLogger()

	// Erfolg: laufender Store -> CompactInPlace gelingt.
	runCompaction(st, logger)

	// Fehler: geschlossener Store -> CompactInPlace schlägt fehl, wird nur geloggt.
	_ = st.Close()
	runCompaction(st, logger)
}

// TestStartHeadroomMonitorRuns startet den Monitor mit winziger Grenze und
// kurzem Intervall, sodass die Goroutine mindestens einen Tick abarbeitet
// (Warnpfad), und beendet ihn über ctx-Cancel. Verifiziert die Goroutine-
// Verdrahtung (Ticker/Select) im Lauf, nicht nur im Noop.
func TestStartHeadroomMonitorRuns(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{DBInitialMB: 1, DBMonitorInterval: time.Millisecond, DBGrowThresholdPct: 0}
	startHeadroomMonitor(ctx, st, cfg, discardLogger())

	// Kurz laufen lassen, damit mindestens ein Tick durchläuft.
	time.Sleep(20 * time.Millisecond)
	cancel()
}
