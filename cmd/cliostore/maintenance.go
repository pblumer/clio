package main

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/store"
)

// remapWarning bewertet, ob der genutzte Daten-Umfang (dataBytes) sich der
// vorbelegten Grenze (initialBytes) so weit genähert hat, dass bbolts teure
// Mmap-Remaps — und damit die Schreib-Latenzspitzen — zurückzukehren drohen.
// Liefert den Füllgrad bezogen auf die Grenze in Prozent und ob gewarnt werden
// soll. Ohne Grenze (initialBytes <= 0) gibt es nichts zu überwachen.
func remapWarning(dataBytes, initialBytes int64, thresholdPct int) (pct float64, warn bool) {
	if initialBytes <= 0 {
		return 0, false
	}
	pct = float64(dataBytes) / float64(initialBytes) * 100
	return pct, pct >= float64(thresholdPct)
}

// startBackgroundMaintenance startet den Hintergrund-Monitor: er beobachtet in
// festem Intervall den Daten-Füllstand gegen die vorbelegte Grenze und warnt —
// einmal je Überschreitung, mit Hysterese gegen Flattern —, bevor bbolt wieder
// remappt. Damit bekommt der Operator rechtzeitig den Hinweis, CLIO_DB_INITIAL_MB
// zu erhöhen (eine spätere Etappe automatisiert das Vergrößern). Der Monitor tut
// nichts, wenn keine Grenze konfiguriert ist (DBInitialMB == 0) oder das Intervall
// 0 ist. Er blockiert nicht und endet, sobald ctx abgebrochen wird.
func startBackgroundMaintenance(ctx context.Context, st *store.Store, cfg config.Config, logger *slog.Logger) {
	if cfg.DBInitialMB <= 0 || cfg.DBMonitorInterval <= 0 {
		return
	}
	initialBytes := int64(cfg.DBInitialMB) << 20
	go func() {
		t := time.NewTicker(cfg.DBMonitorInterval)
		defer t.Stop()
		warned := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				stats, err := st.Stats()
				if err != nil {
					logger.Error("db-monitor: statistik fehlgeschlagen", "err", err)
					continue
				}
				pct, warn := remapWarning(stats.DataBytes, initialBytes, cfg.DBGrowThresholdPct)
				switch {
				case warn && !warned:
					logger.Warn("DB nähert sich der vorbelegten Grenze — Remap-Latenzspitzen drohen; CLIO_DB_INITIAL_MB erhöhen und neu starten",
						"dataBytes", stats.DataBytes, "initialBytes", initialBytes,
						"fillPercent", math.Round(pct*10)/10, "thresholdPct", cfg.DBGrowThresholdPct)
					warned = true
				case warned && pct < float64(cfg.DBGrowThresholdPct)-5:
					// Hysterese: erst deutlich unter der Schwelle wieder entwarnen.
					logger.Info("DB-Füllstand wieder unter der Warn-Schwelle", "fillPercent", math.Round(pct*10)/10)
					warned = false
				}
			}
		}
	}()
}
