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
}
