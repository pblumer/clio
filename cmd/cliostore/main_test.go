package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRunConfigError: fehlendes Token führt zu sofortigem Fehler ohne Start.
func TestRunConfigError(t *testing.T) {
	t.Setenv("CLIO_API_TOKEN", "")

	err := run(context.Background(), quietLogger())
	if err == nil {
		t.Fatal("erwartete fehler bei fehlendem token, bekam nil")
	}
}

// TestRunStoreError: ungültiger DB-Pfad (Verzeichnis) lässt store.Open scheitern.
func TestRunStoreError(t *testing.T) {
	t.Setenv("CLIO_API_TOKEN", "secret")
	t.Setenv("CLIO_DB_PATH", t.TempDir()) // Verzeichnis statt Datei

	err := run(context.Background(), quietLogger())
	if err == nil {
		t.Fatal("erwartete fehler bei ungültigem db-pfad, bekam nil")
	}
}

// TestRunListenError: eine ungültige Listen-Adresse lässt ListenAndServe
// scheitern und run() den Fehler zurückgeben.
func TestRunListenError(t *testing.T) {
	t.Setenv("CLIO_API_TOKEN", "secret")
	t.Setenv("CLIO_ADDR", "127.0.0.1:99999") // Port außerhalb des gültigen Bereichs
	t.Setenv("CLIO_DB_PATH", filepath.Join(t.TempDir(), "listen.db"))

	err := run(context.Background(), quietLogger())
	if err == nil {
		t.Fatal("erwartete fehler bei ungültiger adresse, bekam nil")
	}
}

// TestRunGracefulShutdown: Server startet, Context-Abbruch fährt sauber herunter.
func TestRunGracefulShutdown(t *testing.T) {
	t.Setenv("CLIO_API_TOKEN", "secret")
	t.Setenv("CLIO_ADDR", "127.0.0.1:0")
	t.Setenv("CLIO_DB_PATH", filepath.Join(t.TempDir(), "run.db"))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- run(ctx, quietLogger()) }()

	// Kurz laufen lassen, dann Shutdown auslösen.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown lieferte fehler: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() ist nach shutdown nicht zurückgekehrt")
	}
}
