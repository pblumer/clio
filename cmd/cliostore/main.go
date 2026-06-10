// Command cliostore startet den Event-Store-HTTP-Server.
package main

import (
	"context"
	"errors"
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

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Graceful Shutdown bei SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("server beendet mit fehler", "err", err)
		os.Exit(1)
	}
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

	st, err := store.OpenWithOptions(cfg.DBPath, store.Options{SyncMode: syncMode(cfg.Sync)})
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("store schließen fehlgeschlagen", "err", err)
		}
	}()
	logger.Info("store geöffnet", "path", cfg.DBPath, "sync", cfg.Sync)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(cfg, st, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("cliostore lauscht", "addr", cfg.Addr)
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
