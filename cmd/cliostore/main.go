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

	opts := store.Options{SyncMode: syncMode(cfg.Sync)}
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
	logger.Info("store geöffnet", "path", cfg.DBPath, "sync", cfg.Sync, "signing", signing)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(cfg, st, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
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
