// Command projection-worker baut aus dem clio-Event-Strom ein PostgreSQL-Read-
// Model (CQRS). Es demonstriert die Architekturprinzipien:
//
//   - Event Store = Source of Truth, Projektion = abgeleitetes Read Model
//   - Live-Konsum über observe (History + Live in einem Stream)
//   - persistenter Checkpoint (zuletzt verarbeitete globale Sequenz)
//   - Idempotenz / exactly-once auf dem Read Model (Checkpoint + Daten in EINER Tx)
//   - vollständiger Neuaufbau per Replay (--rebuild)
//   - Lag-/Monitoring-Ausgabe
//
// Bewusst KEIN Teil des clio-Servers (eigenes Modul, eigene DB-Abhängigkeit):
// clio bekommt keine Projection Engine im Kern. Dies ist die offizielle Vorlage,
// wie man ein Read Model sauber außerhalb baut.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	rebuild := flag.Bool("rebuild", false, "Read Model verwerfen und ab Sequenz 0 neu aufbauen (Replay)")
	flag.Parse()

	cfg := loadConfig()
	log.Printf("start: clio=%s subject=%s db=%s", cfg.clioBase, cfg.subject, redactDSN(cfg.databaseURL))

	// Graceful Shutdown bei SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := openDB(ctx, cfg.databaseURL)
	if err != nil {
		log.Fatalf("postgres verbinden: %v", err)
	}
	defer func() { _ = db.Close() }()

	proj := &projection{db: db}
	if err := proj.ensureSchema(ctx); err != nil {
		log.Fatalf("schema: %v", err)
	}
	if *rebuild {
		log.Printf("rebuild: Read Model wird verworfen und neu aufgebaut")
		if err := proj.rebuild(ctx); err != nil {
			log.Fatalf("rebuild: %v", err)
		}
	}

	client := newClioClient(cfg.clioBase, cfg.clioToken)
	go lagMonitor(ctx, client, proj, cfg.subject)

	if err := runWorker(ctx, client, proj, cfg.subject); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("worker: %v", err)
	}
	log.Printf("shutdown: sauber beendet")
}

// runWorker ist die Reconnect-Schleife: ab dem persistenten Checkpoint observen,
// jedes Event anwenden, bei Stream-Ende/-Fehler nach kurzem Backoff neu verbinden
// — immer wieder ab dem aktuellen Checkpoint. So übersteht der Worker
// Netz-/Serverunterbrechungen ohne Datenverlust und ohne Doppelanwendung.
func runWorker(ctx context.Context, client *clioClient, proj *projection, subject string) error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cp, err := proj.checkpoint(ctx)
		if err != nil {
			return err
		}
		log.Printf("observe: verbinde ab sequenz %d", cp+1)

		err = client.observe(ctx, subject, cp+1, func(ev Event) error {
			if err := proj.apply(ctx, ev); err != nil {
				return err
			}
			return nil
		})
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			log.Printf("observe getrennt: %v — reconnect in %s", err, backoff)
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = minDur(backoff*2, maxBackoff)
		default:
			// Server hat den Stream sauber beendet — kurz warten, neu verbinden.
			backoff = time.Second
			if !sleepCtx(ctx, time.Second) {
				return ctx.Err()
			}
		}
	}
}

// lagMonitor protokolliert periodisch den Verarbeitungs-Lag: Differenz zwischen
// der Store-Spitze (eventsTotal aus /info) und dem Checkpoint, plus eine kurze
// Read-Model-Statistik. Rein für die Beobachtbarkeit der Demo.
func lagMonitor(ctx context.Context, client *clioClient, proj *projection, subject string) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cp, err := proj.checkpoint(ctx)
			if err != nil {
				continue
			}
			total, err := client.totalEvents(ctx)
			if err != nil {
				continue
			}
			lag := int64(total) - int64(cp)
			if lag < 0 {
				lag = 0
			}
			log.Printf("lag: storeHead=%d checkpoint=%d lag=%d | %s", total, cp, lag, proj.snapshotLog(ctx))
		}
	}
}

// config bündelt die Laufzeit-Konfiguration aus der Umgebung.
type config struct {
	clioBase    string
	clioToken   string
	databaseURL string
	subject     string
}

func loadConfig() config {
	c := config{
		clioBase:    env("CLIO_BASE", "http://127.0.0.1:3000"),
		clioToken:   os.Getenv("CLIO_TOKEN"),
		databaseURL: env("DATABASE_URL", "postgres://clio:clio@127.0.0.1:5432/clio_readmodel?sslmode=disable"),
		subject:     env("PROJECTION_SUBJECT", "/orders"),
	}
	if c.clioToken == "" {
		log.Fatalf("CLIO_TOKEN ist erforderlich (API-Key kid.secret mit read-Scope)")
	}
	return c
}

func openDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	// Auf die DB warten (Compose startet Worker und Postgres gleichzeitig).
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err = db.PingContext(ctx); err == nil {
			return db, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		if !sleepCtx(ctx, time.Second) {
			return nil, ctx.Err()
		}
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// redactDSN entfernt das Passwort aus dem DSN für die Log-Ausgabe.
func redactDSN(dsn string) string {
	// Sehr simpel: alles zwischen "://user:" und "@" maskieren.
	at := indexByte(dsn, '@')
	col := indexByte(dsn, ':')
	if at < 0 || col < 0 {
		return dsn
	}
	// zweiten ':' (vor dem Passwort) suchen
	rest := dsn[col+1:]
	col2 := indexByte(rest, ':')
	if col2 < 0 || col+1+col2 >= at {
		return dsn
	}
	return dsn[:col+1+col2+1] + "***" + dsn[at:]
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// parseID ist ein kleiner Helfer (in Tests genutzt).
func parseID(s string) (uint64, error) { return strconv.ParseUint(s, 10, 64) }
