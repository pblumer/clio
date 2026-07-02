// Command clio-mcp stellt clios Event-Store über das Model Context Protocol
// (MCP) als native Agent-Tools bereit. Es ist ein dünner Adapter, der Tool-Calls
// in HTTP-Requests gegen eine laufende clio-Instanz übersetzt (ADR-042); die
// Geschäftslogik samt Auth-Scopes bleibt in cliostore.
//
// Standardmäßig spricht es MCP über stdin/stdout (stdio-Transport, für lokale
// Agenten wie Claude Code). Mit -http host:port lauscht es stattdessen unter
// POST /mcp.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pblumer/clio/internal/mcp"
)

// version wird beim Build via -ldflags "-X main.version=..." gesetzt.
var version = "dev"

func main() {
	var (
		httpAddr = flag.String("http", "", "host:port für den HTTP-Transport (leer = stdio)")
		baseURL  = flag.String("base-url", getenvDefault("CLIO_BASE_URL", "http://127.0.0.1:3000"), "Basis-URL der clio-Instanz")
		token    = flag.String("token", os.Getenv("CLIO_API_TOKEN"), "clio-API-Key als kid.secret (Fallback-Identität)")
		showVer  = flag.Bool("version", false, "Version ausgeben und beenden")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("clio-mcp", version)
		return
	}

	// stdout ist im stdio-Modus der Protokoll-Stream — Logging MUSS nach stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	client := mcp.NewClient(*baseURL, *token, nil)
	srv := mcp.NewServer(client, mcp.WithVersion(version))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *httpAddr != "" {
		if err := serveHTTP(ctx, srv, *httpAddr, logger); err != nil {
			logger.Error("clio-mcp (http) beendet mit fehler", "err", err)
			os.Exit(1)
		}
		return
	}

	logger.Info("clio-mcp: stdio-transport", "baseURL", *baseURL, "version", version)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		logger.Error("clio-mcp (stdio) beendet mit fehler", "err", err)
		os.Exit(1)
	}
}

// serveHTTP startet den HTTP-Transport unter POST /mcp mit Graceful Shutdown.
func serveHTTP(ctx context.Context, srv *mcp.Server, addr string, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.Handle("POST /mcp", srv)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("clio-mcp: http-transport", "addr", addr, "path", "/mcp", "version", version)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
