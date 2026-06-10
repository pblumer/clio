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
}

// Environment-Variablen, aus denen die Konfiguration gelesen wird.
const (
	envAddr   = "CLIO_ADDR"
	envToken  = "CLIO_API_TOKEN"
	envDBPath = "CLIO_DB_PATH"

	defaultAddr   = ":3000"
	defaultDBPath = "clio.db"
)

// FromEnv liest die Konfiguration aus Umgebungsvariablen und validiert sie.
// CLIO_API_TOKEN ist Pflicht; CLIO_ADDR ist optional (Default :3000).
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:     getenvDefault(envAddr, defaultAddr),
		APIToken: os.Getenv(envToken),
		DBPath:   getenvDefault(envDBPath, defaultDBPath),
	}

	if cfg.APIToken == "" {
		return Config{}, fmt.Errorf("%s muss gesetzt sein", envToken)
	}

	return cfg, nil
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
