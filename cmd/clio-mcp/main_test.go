package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/mcp"
)

func TestGetenvDefault(t *testing.T) {
	t.Setenv("CLIO_MCP_TEST_VAR", "")
	if got := getenvDefault("CLIO_MCP_TEST_VAR", "fallback"); got != "fallback" {
		t.Errorf("leer → %q, erwartet fallback", got)
	}
	t.Setenv("CLIO_MCP_TEST_VAR", "gesetzt")
	if got := getenvDefault("CLIO_MCP_TEST_VAR", "fallback"); got != "gesetzt" {
		t.Errorf("gesetzt → %q, erwartet gesetzt", got)
	}
}

// TestServeHTTPRoundtripUndShutdown startet den HTTP-Transport auf einem freien
// Port, setzt einen JSON-RPC-initialize ab und prüft, dass ctx-Abbruch einen
// sauberen Shutdown (Rückgabe nil) auslöst.
func TestServeHTTPRoundtripUndShutdown(t *testing.T) {
	srv := mcp.NewServer(mcp.NewClient("http://clio", "", mcp.HandlerTransport(http.NewServeMux())), mcp.WithVersion("test"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, srv, "127.0.0.1:38999", logger) }()

	// Kurz warten, bis der Listener steht, dann einen initialize absetzen.
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := http.Post("http://127.0.0.1:38999/mcp", "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("initialize → HTTP %d, erwartet 200", resp.StatusCode)
			}
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("Listener nicht erreichbar: %v", lastErr)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveHTTP nach Shutdown → %v, erwartet nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTP hat nach ctx-Abbruch nicht beendet")
	}
}
