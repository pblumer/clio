package main

import (
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pblumer/clio/internal/store"
)

// verifyKeyFromEnv leitet aus CLIO_SIGNING_KEY (falls gesetzt) den öffentlichen
// Ed25519-Schlüssel ab, mit dem Offline-`verify`/`backup --verify` auch die
// Event-Signaturen prüft. Ohne Schlüssel wird die Kette ohne Signaturprüfung
// nachgerechnet (nil).
func verifyKeyFromEnv() (ed25519.PublicKey, error) {
	seed := os.Getenv("CLIO_SIGNING_KEY")
	if seed == "" {
		return nil, nil
	}
	key, err := store.ParsePrivateKey(seed)
	if err != nil {
		return nil, fmt.Errorf("CLIO_SIGNING_KEY: %w", err)
	}
	return key.Public().(ed25519.PublicKey), nil
}

// runBackup implementiert `cliostore backup` — ein konsistenter Offline-Snapshot
// einer (nicht von einem laufenden Server gehaltenen) DB. Optional `--verify`
// prüft das frische Backup direkt nach dem Schreiben.
func runBackup(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "Pfad zur Quell-Datenbank")
	output := fs.String("output", "", "Zieldatei für den Snapshot (z. B. clio-2026-06-18.clio)")
	force := fs.Bool("force", false, "vorhandene Zieldatei überschreiben")
	doVerify := fs.Bool("verify", false, "Backup nach dem Schreiben verifizieren (Hash-Kette)")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output ist erforderlich")
	}

	start := time.Now()
	res, err := store.BackupFile(*db, *output, *force)
	if err != nil {
		return err
	}
	durMs := time.Since(start).Milliseconds()

	verified := false
	if *doVerify {
		key, kerr := verifyKeyFromEnv()
		if kerr != nil {
			return kerr
		}
		vr, verr := store.VerifyFile(*output, key)
		if verr != nil {
			return fmt.Errorf("verify: %w", verr)
		}
		if !vr.OK {
			return fmt.Errorf("backup ist NICHT verifizierbar: %s (brokenAt=%s)", vr.Reason, vr.BrokenAt)
		}
		verified = true
	}

	if *asJSON {
		return writeJSONLine(out, map[string]any{
			"output":     *output,
			"bytes":      res.Bytes,
			"events":     res.Events,
			"head":       res.Head,
			"durationMs": durMs,
			"verified":   verified,
		})
	}
	fmt.Fprintf(out, "backup: %s — %d events, %d bytes, head %s (%d ms)%s\n",
		*output, res.Events, res.Bytes, shortHead(res.Head), durMs, verifiedSuffix(verified))
	return nil
}

// runRestore implementiert `cliostore restore` — spielt ein Backup an einen
// Zielpfad ein (offline; existierendes Ziel nur mit `--force`).
func runRestore(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(out)
	input := fs.String("input", "", "einzuspielendes Backup (.clio)")
	db := fs.String("db", dbPath(), "Ziel-Datenbankpfad")
	force := fs.Bool("force", false, "vorhandene Ziel-DB überschreiben")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return fmt.Errorf("--input ist erforderlich")
	}

	start := time.Now()
	res, err := store.Restore(*input, *db, *force)
	if err != nil {
		return err
	}
	durMs := time.Since(start).Milliseconds()

	if *asJSON {
		return writeJSONLine(out, map[string]any{
			"input":      *input,
			"db":         *db,
			"events":     res.Events,
			"head":       res.Head,
			"durationMs": durMs,
		})
	}
	fmt.Fprintf(out, "restore: %s -> %s — %d events, head %s (%d ms)\n"+
		"hinweis: jetzt `cliostore verify --db %s` ausführen\n",
		*input, *db, res.Events, shortHead(res.Head), durMs, *db)
	return nil
}

// runVerify implementiert `cliostore verify` — rechnet die Hash-Kette einer DB
// offline nach. Liefert einen Fehler (Exit-Code 1), wenn die Kette gebrochen ist,
// damit Backup-Audits/CI das skripten können.
func runVerify(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", dbPath(), "zu prüfende Datenbank-/Backup-Datei")
	asJSON := fs.Bool("json", false, "Ergebnis als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return err
	}

	key, err := verifyKeyFromEnv()
	if err != nil {
		return err
	}
	res, err := store.VerifyFile(*db, key)
	if err != nil {
		return err
	}

	if *asJSON {
		if err := writeJSONLine(out, res); err != nil {
			return err
		}
	} else if res.OK {
		fmt.Fprintf(out, "verify: OK — %d events, head %s\n", res.Count, shortHead(res.Head))
	} else {
		fmt.Fprintf(out, "verify: KETTE GEBROCHEN — %s (brokenAt=%s, geprüft=%d)\n",
			res.Reason, res.BrokenAt, res.Count)
	}
	if !res.OK {
		// Nicht-OK ist ein skriptbares Fehlerergebnis (Exit-Code 1).
		return fmt.Errorf("hash-kette gebrochen")
	}
	return nil
}

// writeJSONLine schreibt body als eine eingerückte JSON-Zeile.
func writeJSONLine(out io.Writer, body any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(body)
}

// shortHead kürzt den Hash-Kopf für die menschenlesbare Ausgabe.
func shortHead(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

func verifiedSuffix(v bool) string {
	if v {
		return " [verifiziert]"
	}
	return ""
}
