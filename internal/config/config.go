// Package config lädt die Laufzeitkonfiguration von cliostore aus der Umgebung.
package config

import (
	"fmt"
	"os"
)

// Config bündelt alle Laufzeit-Einstellungen des Servers.
type Config struct {
	// Addr ist die Listen-Adresse des HTTP-Servers, z. B. ":3000".
	Addr string

	// APIToken ist das Bearer-Token, das geschützte Routen absichert
	// (siehe ADR-008). Pflichtfeld.
	APIToken string

	// DBPath ist der Pfad zur bbolt-Datenbankdatei (ADR-006).
	DBPath string

	// Sync steuert die Durability-/Performance-Abwägung beim Schreiben:
	// "group" (Default, Group Commit), "always" (fsync pro Write) oder
	// "off" (kein fsync, maximaler Durchsatz).
	Sync string

	// SigningKey ist ein optionaler base64-kodierter Ed25519-Schlüssel. Ist er
	// gesetzt, werden Events signiert (Authentizität).
	SigningKey string
}

// Environment-Variablen, aus denen die Konfiguration gelesen wird.
const (
	envAddr    = "CLIO_ADDR"
	envToken   = "CLIO_API_TOKEN"
	envDBPath  = "CLIO_DB_PATH"
	envSync    = "CLIO_SYNC"
	envSignKey = "CLIO_SIGNING_KEY"

	defaultAddr   = ":3000"
	defaultDBPath = "clio.db"
	defaultSync   = "group"
)

// validSync enthält die erlaubten Werte für CLIO_SYNC.
var validSync = map[string]bool{"group": true, "always": true, "off": true}

// FromEnv liest die Konfiguration aus Umgebungsvariablen und validiert sie.
// CLIO_API_TOKEN ist Pflicht; übrige Variablen sind optional mit Defaults.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:       getenvDefault(envAddr, defaultAddr),
		APIToken:   os.Getenv(envToken),
		DBPath:     getenvDefault(envDBPath, defaultDBPath),
		Sync:       getenvDefault(envSync, defaultSync),
		SigningKey: os.Getenv(envSignKey),
	}

	if cfg.APIToken == "" {
		return Config{}, fmt.Errorf("%s muss gesetzt sein", envToken)
	}
	if !validSync[cfg.Sync] {
		return Config{}, fmt.Errorf("%s muss group, always oder off sein, war %q", envSync, cfg.Sync)
	}

	return cfg, nil
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
