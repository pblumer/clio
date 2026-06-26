// Command cliostore startet den Event-Store-HTTP-Server.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pblumer/clio/internal/config"
	"github.com/pblumer/clio/internal/httpapi"
	"github.com/pblumer/clio/internal/store"
)

// version wird beim Build via -ldflags "-X main.version=..." gesetzt.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			fmt.Println("cliostore", version)
			return
		case "compact":
			if err := runCompact(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "compact:", err)
				os.Exit(1)
			}
			return
		case "gen-key":
			if err := runGenKey(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "gen-key:", err)
				os.Exit(1)
			}
			return
		case "backup":
			if err := runBackup(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "backup:", err)
				os.Exit(1)
			}
			return
		case "restore":
			if err := runRestore(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "restore:", err)
				os.Exit(1)
			}
			return
		case "verify":
			if err := runVerify(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "verify:", err)
				os.Exit(1)
			}
			return
		case "keys":
			if err := runKeys(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "keys:", err)
				os.Exit(1)
			}
			return
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Graceful Shutdown bei SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("server beendet mit fehler", "err", err)
		os.Exit(1)
	}
}

// dbPath liefert den DB-Pfad aus der Umgebung (Default clio.db).
func dbPath() string {
	if p := os.Getenv("CLIO_DB_PATH"); p != "" {
		return p
	}
	return "clio.db"
}

// runGenKey erzeugt ein neues Ed25519-Schlüsselpaar zum Signieren von Events.
func runGenKey(w io.Writer) error {
	seed, pub, err := store.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "CLIO_SIGNING_KEY=%s\n", seed)
	fmt.Fprintf(w, "# public key (zum Verifizieren): %s\n", pub)
	return nil
}

// runCompact kompaktiert die Datenbankdatei (offline) und meldet die Größen.
func runCompact(w io.Writer) error {
	path := dbPath()
	old, neu, err := store.Compact(path)
	if err != nil {
		return err
	}
	var pct float64
	if old > 0 {
		pct = 100 * (1 - float64(neu)/float64(old))
	}
	fmt.Fprintf(w, "kompaktiert: %s — %d -> %d bytes (%.1f%% kleiner)\n", path, old, neu, pct)
	return nil
}

// syncMode übersetzt den (bereits validierten) Config-Wert in store.SyncMode.
func syncMode(s string) store.SyncMode {
	switch s {
	case "always":
		return store.SyncAlways
	case "off":
		return store.SyncOff
	default:
		return store.SyncGroup
	}
}

// run startet den Server und blockiert, bis ctx abgebrochen wird (Graceful
// Shutdown) oder der Server mit einem Fehler endet.
func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	opts := store.Options{
		SyncMode:        syncMode(cfg.Sync),
		Compress:        cfg.Compress,
		DataIndexFields: cfg.DataIndexFields,
		Partitions:      cfg.Partitions,
		PartitionVNodes: cfg.PartitionVNodes,
	}
	if cfg.DBInitialMB > 0 {
		opts.InitialMmapSize = cfg.DBInitialMB << 20
	}
	signing := false
	if cfg.SigningKey != "" {
		key, err := store.ParsePrivateKey(cfg.SigningKey)
		if err != nil {
			return fmt.Errorf("CLIO_SIGNING_KEY: %w", err)
		}
		opts.SigningKey = key
		signing = true
	}

	st, err := store.OpenWithOptions(cfg.DBPath, opts)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("store schließen fehlgeschlagen", "err", err)
		}
	}()
	logger.Info("store geöffnet", "path", cfg.DBPath, "sync", cfg.Sync, "signing", signing, "compress", cfg.Compress, "initialMB", cfg.DBInitialMB, "partitions", cfg.Partitions)

	// Auth-Material sicherstellen (ADR-025): bei leerem Schlüsselbund aus dem
	// Bootstrap-/Legacy-ENV einen Admin-Key anlegen, sonst Start verweigern.
	if err := bootstrapAuth(st, cfg, logger); err != nil {
		return err
	}

	if cfg.DevMode {
		logger.Warn("DEV-MODE aktiv — destruktiver DB-Reset unter POST /api/v1/dev/reset-database freigeschaltet (nicht in Produktion verwenden)")
	}

	// Hintergrund-Monitor: warnt vor Annäherung an die vorbelegte DB-Grenze
	// (läuft nur bei gesetztem CLIO_DB_INITIAL_MB). Endet mit ctx beim Shutdown.
	startBackgroundMaintenance(ctx, st, cfg, logger)

	api := httpapi.New(
		cfg,
		st,
		logger,
		httpapi.WithBuildInfo(version, time.Now().UTC()),
	)
	// Hintergrundaufgaben des API-Layers (Presence-Sweeper, ADR-030) starten; sie
	// enden mit ctx beim Graceful Shutdown.
	api.StartBackground(ctx)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Handler(),
		// ReadHeaderTimeout schützt gegen Slowloris auf den Headern.
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout begrenzt hängende/langsame Antworten und gibt so blockierte
		// Goroutinen/Verbindungen frei. Die streamenden Handler (observe-events und
		// die Lese-Routen) heben ihre eigene Schreib-Deadline per
		// http.ResponseController bewusst wieder auf — sonst würde ein langer
		// Live-Stream bzw. ein großer Read fälschlich gekappt.
		WriteTimeout: 30 * time.Second,
		// IdleTimeout schließt im Leerlauf gehaltene Keep-Alive-Verbindungen.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("cliostore lauscht", "addr", cfg.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal empfangen, fahre herunter")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
