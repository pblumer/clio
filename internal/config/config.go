// Package config lädt die Laufzeitkonfiguration von cliostore aus der Umgebung.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config bündelt alle Laufzeit-Einstellungen des Servers.
type Config struct {
	// Addr ist die Listen-Adresse des HTTP-Servers, z. B. ":3000".
	Addr string

	// APIToken ist das altgediente, geteilte Bearer-Token (ADR-008). Mit dem
	// Schlüsselbund (ADR-025) ist es **deprecated** und nicht mehr Pflicht: ist
	// es gesetzt und der Schlüsselbund leer, wird daraus beim Start ein einzelner
	// Admin-Key gebootet (Legacy-Kompatibilität, siehe WP-05).
	APIToken string

	// BootstrapAdminKey ist das optionale Klartext-Geheimnis (CLIO_BOOTSTRAP_ADMIN_KEY),
	// aus dem beim ersten Start — und nur bei leerem Schlüsselbund — ein
	// initialer Admin-Key erzeugt wird (löst das Henne-Ei-Problem, ADR-025).
	BootstrapAdminKey string

	// DBPath ist der Pfad zur bbolt-Datenbankdatei (ADR-006).
	DBPath string

	// DBInitialMB legt die anfängliche Mmap-Größe der bbolt-Datei in MiB fest
	// (CLIO_DB_INITIAL_MB). bbolt mappt die Datei beim Wachsen neu und hält dabei
	// kurz einen exklusiven Lock — bei großen, gefüllten Datenbanken unter Leselast
	// erzeugt das spürbare Schreib-Latenzspitzen. Wird die Mmap vorab groß genug
	// dimensioniert, entfallen diese Remaps. Zusätzlich wird die Datei real auf
	// diese Größe vorbelegt. 0 (Default) = bisheriges Verhalten (dynamisches
	// Wachsen ab winziger Datei). Niemals verkleinernd: ist die DB schon größer,
	// bleibt sie unangetastet.
	DBInitialMB int

	// DBMonitorInterval steuert den Hintergrund-Monitor, der den Daten-Füllstand
	// gegen die vorbelegte Grenze (DBInitialMB) beobachtet und warnt, bevor die
	// teuren bbolt-Remaps zurückkehren (CLIO_DB_MONITOR_INTERVAL als Go-Dauer,
	// Default 60s; 0 schaltet den Monitor ab). Er läuft ohnehin nur, wenn
	// DBInitialMB gesetzt ist (sonst gibt es keine Grenze zu überwachen).
	DBMonitorInterval time.Duration

	// DBGrowThresholdPct ist der Schwellwert (Prozent der vorbelegten Größe), ab
	// dem der Monitor warnt (CLIO_DB_GROW_THRESHOLD_PCT). Eine spätere Etappe nutzt
	// denselben Schwellwert, um automatisch zu vergrößern (daher der Name). Geklemmt
	// auf [1,99]; Default 80.
	DBGrowThresholdPct int

	// DBCompactEnabled schaltet die Online-Hintergrund-Kompaktierung ein
	// (CLIO_DB_COMPACT_ENABLED). Defragmentiert die DB periodisch im laufenden
	// Betrieb (kurze Downtime pro Lauf, ADR-015). Default aus.
	DBCompactEnabled bool

	// DBCompactIntervalH ist das Intervall der Hintergrund-Kompaktierung in Stunden
	// (CLIO_DB_COMPACT_INTERVAL_H). Geklemmt auf [1, 168]; Default 6.
	DBCompactIntervalH int

	// Sync steuert die Durability-/Performance-Abwägung beim Schreiben:
	// "group" (Default, Group Commit), "always" (fsync pro Write) oder
	// "off" (kein fsync, maximaler Durchsatz).
	Sync string

	// SigningKey ist ein optionaler base64-kodierter Ed25519-Schlüssel. Ist er
	// gesetzt, werden Events signiert (Authentizität).
	SigningKey string

	// Compress aktiviert die transparente DEFLATE-Kompression der gespeicherten
	// Event-Werte (ADR-024). Default aus; per CLIO_COMPRESS einschaltbar. Wirkt nur
	// auf neu geschriebene Events — bestehende bleiben lesbar.
	Compress bool

	// EventAuthorship übernimmt (wenn aktiv) die authentifizierte Identität (kid)
	// als CloudEvents-Extension `clioauthkid` in jedes geschriebene Event
	// (Urheberschaft, ADR-025). Default aus; per CLIO_EVENT_AUTHORSHIP aktivierbar.
	// Append-only-konform (neues Attribut auf neuen Events) und in Hash/Signatur
	// gebunden — wirkt nur auf neu geschriebene Events.
	EventAuthorship bool

	// DevMode schaltet Entwickler-Komfort frei, der im Produktivbetrieb nichts zu
	// suchen hat — allen voran das destruktive Zurücksetzen der Datenbank über
	// POST /api/v1/dev/reset-database und den dazugehörigen Button im Dashboard
	// (ADR-022). Standardmäßig aus; nur explizit per CLIO_DEV_MODE aktivierbar.
	DevMode bool

	// ObservePreambleBytes ist die Größe des Anti-Buffering-Polsters (Whitespace),
	// das ein observe-Stream einmalig beim Verbindungsaufbau sendet. Manche
	// puffernden Reverse-Proxies/Security-Gateways geben einen Stream erst weiter,
	// wenn genug Bytes geflossen sind — ein ausreichend großes Polster kippt sie in
	// den Streaming-Modus. Per CLIO_OBSERVE_PREAMBLE_BYTES einstellbar; 0 schaltet
	// das Polster ab. Vom Client als Leerzeile ignoriert.
	ObservePreambleBytes int

	// QueryTimeout begrenzt die Laufzeit einer einzelnen run-query-Auswertung
	// (Scan + Prädikat). Ein selektives Prädikat ohne Typ-Constraint scannt den
	// gesamten Scope und hält dabei eine bbolt-Lesetransaktion — unter Schreiblast
	// blockiert das die Wiederverwendung freier Seiten (DB-/Speicherwachstum). Die
	// Deadline bricht solche Scans sauber ab, statt die Verbindung hängen zu
	// lassen. Per CLIO_QUERY_TIMEOUT als Go-Dauer (z. B. "30s", "2m") einstellbar;
	// 0 (Default) schaltet die Deadline ab — rückwärtskompatibel zum bisherigen,
	// unbegrenzten Verhalten. Der run-query-Stream sendet unabhängig davon einen
	// Heartbeat, der die Proxy-Verbindung während langer Scans offen hält.
	QueryTimeout time.Duration

	// DataIndexFields deklariert pro Event-Typ die `event.data`-Felder, die in
	// einen internen Sekundärindex aufgenommen werden (ADR-029). Ein
	// `event.data.<feld> == '<wert>'`-Prädikat über einen so indizierten Typ wird
	// dann per Index-Range-Scan beantwortet statt per vollständigem Typ-Scan mit
	// Payload-Deserialisierung (der teure Pfad aus ADR-028). Per
	// CLIO_DATA_INDEX_FIELDS als kommagetrennte `typ:feld`-Liste konfigurierbar
	// (z. B. "identity.employee.new.v2:department,identity.employee.new.v2:lastName").
	// Leer (Default) = kein Feld indiziert → vollständig rückwärtskompatibel.
	// Nur Top-Level-Felder mit String-Wert (v1); verschachtelte Pfade und
	// numerische Werte sind bewusst nicht abgedeckt.
	DataIndexFields map[string][]string
}

// Environment-Variablen, aus denen die Konfiguration gelesen wird.
const (
	envAddr      = "CLIO_ADDR"
	envToken     = "CLIO_API_TOKEN"
	envBootstrap = "CLIO_BOOTSTRAP_ADMIN_KEY"
	envDBPath    = "CLIO_DB_PATH"
	envDBInitMB  = "CLIO_DB_INITIAL_MB"
	envDBMonInt  = "CLIO_DB_MONITOR_INTERVAL"
	envDBGrowPct = "CLIO_DB_GROW_THRESHOLD_PCT"
	envDBCompact = "CLIO_DB_COMPACT_ENABLED"
	envDBCompInt = "CLIO_DB_COMPACT_INTERVAL_H"
	envSync      = "CLIO_SYNC"
	envSignKey   = "CLIO_SIGNING_KEY"
	envDevMode   = "CLIO_DEV_MODE"
	envCompress  = "CLIO_COMPRESS"
	envEventAuth = "CLIO_EVENT_AUTHORSHIP"
	envObsvPre   = "CLIO_OBSERVE_PREAMBLE_BYTES"
	envQueryTO   = "CLIO_QUERY_TIMEOUT"
	envDataIdx   = "CLIO_DATA_INDEX_FIELDS"

	defaultAddr    = ":3000"
	defaultDBPath  = "clio.db"
	defaultSync    = "group"
	defaultObsvPre = 4096 // Anti-Buffering-Polster für observe (siehe Config-Feld)
	maxObsvPre     = 1 << 20

	// maxInitMB deckelt CLIO_DB_INITIAL_MB auf 64 TiB — großzügig genug für jede
	// reale Platte, schützt aber vor versehentlichen Tippfehlern (und Overflow).
	maxInitMB = 64 << 20

	defaultMonInterval = 60 * time.Second
	defaultGrowPct     = 80
	defaultCompactH    = 6
	maxCompactH        = 168 // eine Woche
)

// validSync enthält die erlaubten Werte für CLIO_SYNC.
var validSync = map[string]bool{"group": true, "always": true, "off": true}

// FromEnv liest die Konfiguration aus Umgebungsvariablen und validiert sie.
// Mit dem Schlüsselbund (ADR-025) ist CLIO_API_TOKEN nicht mehr Pflicht: das
// Vorhandensein von Auth-Material (nicht-leerer Bund, CLIO_BOOTSTRAP_ADMIN_KEY
// oder CLIO_API_TOKEN) wird beim Start geprüft, sobald der Store offen ist
// (WP-05) — denn dafür muss der Bucket gelesen werden. Übrige Variablen sind
// optional mit Defaults.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:                 getenvDefault(envAddr, defaultAddr),
		APIToken:             os.Getenv(envToken),
		BootstrapAdminKey:    os.Getenv(envBootstrap),
		DBPath:               getenvDefault(envDBPath, defaultDBPath),
		DBInitialMB:          parseIntDefault(envDBInitMB, 0, 0, maxInitMB),
		DBGrowThresholdPct:   parseIntDefault(envDBGrowPct, defaultGrowPct, 1, 99),
		DBCompactEnabled:     parseBoolDefault(envDBCompact, false),
		DBCompactIntervalH:   parseIntDefault(envDBCompInt, defaultCompactH, 1, maxCompactH),
		Sync:                 getenvDefault(envSync, defaultSync),
		SigningKey:           os.Getenv(envSignKey),
		DevMode:              parseBoolDefault(envDevMode, false),
		Compress:             parseBoolDefault(envCompress, false),
		EventAuthorship:      parseBoolDefault(envEventAuth, false),
		ObservePreambleBytes: parseIntDefault(envObsvPre, defaultObsvPre, 0, maxObsvPre),
	}

	if !validSync[cfg.Sync] {
		return Config{}, fmt.Errorf("%s muss group, always oder off sein, war %q", envSync, cfg.Sync)
	}

	to, err := parseDurationDefault(envQueryTO, 0)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryTimeout = to

	mon, err := parseDurationDefault(envDBMonInt, defaultMonInterval)
	if err != nil {
		return Config{}, err
	}
	cfg.DBMonitorInterval = mon

	fields, err := parseDataIndexFields(os.Getenv(envDataIdx))
	if err != nil {
		return Config{}, err
	}
	cfg.DataIndexFields = fields

	return cfg, nil
}

// parseDataIndexFields liest die kommagetrennte `typ:feld`-Liste aus
// CLIO_DATA_INDEX_FIELDS (ADR-029) und gruppiert sie je Typ. Leer ergibt nil
// (kein Feld indiziert). Ein Eintrag ohne genau ein ':' oder mit leerem Typ/Feld
// ist ein Fehler — lieber laut scheitern als still ein Feld nicht indizieren.
// Doppelte `typ:feld`-Einträge werden zusammengefasst (idempotent).
func parseDataIndexFields(v string) (map[string][]string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	out := make(map[string][]string)
	seen := make(map[string]struct{})
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		typ, field, ok := strings.Cut(entry, ":")
		typ, field = strings.TrimSpace(typ), strings.TrimSpace(field)
		if !ok || typ == "" || field == "" {
			return nil, fmt.Errorf("%s: Eintrag %q muss die Form \"typ:feld\" haben", envDataIdx, entry)
		}
		key := typ + "\x00" + field
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out[typ] = append(out[typ], field)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// parseDurationDefault liest eine Go-Dauer (z. B. "30s", "2m") aus der Umgebung.
// Leer ergibt fallback; ein unlesbarer oder negativer Wert ist ein Fehler (lieber
// laut scheitern als still eine kaputte Deadline übernehmen).
func parseDurationDefault(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s muss eine Go-Dauer sein (z. B. \"30s\"), war %q", key, v)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s darf nicht negativ sein, war %q", key, v)
	}
	return d, nil
}

// parseIntDefault liest eine nicht-negative Ganzzahl aus der Umgebung und
// begrenzt sie auf [min,max]. Leer oder unlesbar ergibt fallback.
func parseIntDefault(key string, fallback, min, max int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}

// parseBoolDefault liest einen Wahrheitswert aus der Umgebung (akzeptiert
// 1/t/true/0/f/false … wie strconv.ParseBool). Leer oder unlesbar ergibt
// fallback — so bleibt der Dev-Mode aus, solange er nicht bewusst gesetzt wird.
func parseBoolDefault(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
