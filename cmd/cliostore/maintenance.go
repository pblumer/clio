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

// startBackgroundMaintenance startet — je nach Konfiguration — bis zu zwei
// Hintergrund-Goroutinen: den Headroom-Monitor und den Compaction-Scheduler.
// Beide blockieren nicht und enden, sobald ctx abgebrochen wird.
func startBackgroundMaintenance(ctx context.Context, st *store.Store, cfg config.Config, logger *slog.Logger) {
	startHeadroomMonitor(ctx, st, cfg, logger)
	startCompactScheduler(ctx, st, cfg, logger)
}

// startHeadroomMonitor beobachtet in festem Intervall den Daten-Füllstand gegen
// die vorbelegte Grenze und warnt — einmal je Überschreitung, mit Hysterese gegen
// Flattern —, bevor bbolt wieder remappt. Damit bekommt der Operator rechtzeitig
// den Hinweis, CLIO_DB_INITIAL_MB zu erhöhen. Tut nichts, wenn keine Grenze
// konfiguriert ist (DBInitialMB == 0) oder das Intervall 0 ist.
func startHeadroomMonitor(ctx context.Context, st *store.Store, cfg config.Config, logger *slog.Logger) {
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
				warned = headroomTick(st, initialBytes, cfg, logger, warned)
			}
		}
	}()
}

// headroomTick verarbeitet einen einzelnen Monitor-Tick: Es liest den
// Daten-Füllstand und meldet — mit Hysterese gegen Flattern — das Über- bzw.
// Unterschreiten der Warn-Schwelle. Zurückgegeben wird der fortgeschriebene
// warned-Zustand (true = es wurde bereits gewarnt, noch nicht entwarnt), den der
// Aufrufer in den nächsten Tick mitnimmt. Ein Statistik-Fehler lässt den Zustand
// unverändert (nur Log), damit ein transienter Fehler keine falsche Entwarnung
// auslöst.
func headroomTick(st *store.Store, initialBytes int64, cfg config.Config, logger *slog.Logger, warned bool) bool {
	stats, err := st.Stats()
	if err != nil {
		logger.Error("db-monitor: statistik fehlgeschlagen", "err", err)
		return warned
	}
	pct, warn := remapWarning(stats.DataBytes, initialBytes, cfg.DBGrowThresholdPct)
	switch {
	case warn && !warned:
		logger.Warn("DB nähert sich der vorbelegten Grenze — Remap-Latenzspitzen drohen; CLIO_DB_INITIAL_MB erhöhen und neu starten",
			"dataBytes", stats.DataBytes, "initialBytes", initialBytes,
			"fillPercent", math.Round(pct*10)/10, "thresholdPct", cfg.DBGrowThresholdPct)
		return true
	case warned && pct < float64(cfg.DBGrowThresholdPct)-5:
		// Hysterese: erst deutlich unter der Schwelle wieder entwarnen.
		logger.Info("DB-Füllstand wieder unter der Warn-Schwelle", "fillPercent", math.Round(pct*10)/10)
		return false
	}
	return warned
}

// startCompactScheduler kompaktiert die DB periodisch online (CompactInPlace,
// ADR-015), wenn CLIO_DB_COMPACT_ENABLED gesetzt ist. Pro Lauf gibt es eine kurze
// Downtime (alle Zugriffe blockieren, bis der Reopen durch ist). Hinweis: ist die
// Datei vorbelegt (CLIO_DB_INITIAL_MB), wird sie nach dem Compact wieder auf die
// reservierte Größe gebracht — die gemeldete Verkleinerung bezieht sich dann auf
// die defragmentierte Datei vor der erneuten Vorbelegung.
func startCompactScheduler(ctx context.Context, st *store.Store, cfg config.Config, logger *slog.Logger) {
	if !cfg.DBCompactEnabled || cfg.DBCompactIntervalH <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(time.Duration(cfg.DBCompactIntervalH) * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runCompaction(st, logger)
			}
		}
	}()
}

// runCompaction führt eine einzelne Online-Kompaktierung aus und protokolliert
// das Ergebnis — die erreichte Verkleinerung in Prozent — bzw. den Fehler. Bei
// einem Fehler bleibt die DB nutzbar (CompactInPlace stellt das sicher); hier
// wird nur geloggt, damit der Scheduler weiterläuft.
func runCompaction(st *store.Store, logger *slog.Logger) {
	old, neu, err := st.CompactInPlace()
	if err != nil {
		logger.Error("hintergrund-compact fehlgeschlagen", "err", err)
		return
	}
	var pct float64
	if old > 0 {
		pct = 100 * (1 - float64(neu)/float64(old))
	}
	logger.Info("hintergrund-compact abgeschlossen",
		"oldBytes", old, "newBytes", neu, "kleinerProzent", math.Round(pct*10)/10)
}
