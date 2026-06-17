package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRunNoAuthMaterial: ohne jedes Auth-Material (kein Token, kein
// Bootstrap-Key, leerer Bund) verweigert der Start (ADR-025, Bootstrap).
func TestRunNoAuthMaterial(t *testing.T) {
	t.Setenv("CLIO_API_TOKEN", "")
	t.Setenv("CLIO_BOOTSTRAP_ADMIN_KEY", "")
	t.Setenv("CLIO_DB_PATH", filepath.Join(t.TempDir(), "noauth.db"))

	err := run(context.Background(), quietLogger())
	if err == nil {
		t.Fatal("erwartete fehler bei fehlendem auth-material, bekam nil")
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

func TestRunCompact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t"}}, nil); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = st.Close()

	t.Setenv("CLIO_DB_PATH", path)
	var buf bytes.Buffer
	if err := runCompact(&buf); err != nil {
		t.Fatalf("runCompact: %v", err)
	}
	if !strings.Contains(buf.String(), "kompaktiert") {
		t.Fatalf("ausgabe = %q", buf.String())
	}
}

func TestRunGenKey(t *testing.T) {
	var buf bytes.Buffer
	if err := runGenKey(&buf); err != nil {
		t.Fatalf("runGenKey: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "CLIO_SIGNING_KEY=") || !strings.Contains(out, "public key") {
		t.Fatalf("ausgabe = %q", out)
	}
	// Der ausgegebene Schlüssel muss parsebar sein.
	seed := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(out, "\n", 2)[0], "CLIO_SIGNING_KEY="))
	if _, err := store.ParsePrivateKey(seed); err != nil {
		t.Fatalf("generierter schlüssel nicht parsebar: %v", err)
	}
}

func TestSyncMode(t *testing.T) {
	tests := map[string]store.SyncMode{
		"group":   store.SyncGroup,
		"always":  store.SyncAlways,
		"off":     store.SyncOff,
		"unknown": store.SyncGroup, // Fallback
	}
	for in, want := range tests {
		if got := syncMode(in); got != want {
			t.Errorf("syncMode(%q) = %v, want %v", in, got, want)
		}
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
