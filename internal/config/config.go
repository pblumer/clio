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

	// DBPath ist der Pfad zur bbolt-Datenbankdatei (ADR-006). Bei N>1 Partitionen
	// (siehe Partitions) ist dies die Datei der Partition 0 (zugleich Träger der
	// zentralen Buckets); die übrigen Partitionen liegen in Geschwisterdateien
	// `<DBPath>.p1`, `<DBPath>.p2`, … (ADR-037).
	DBPath string

	// Partitions ist die Anzahl der Partitionen, über die der Event-Strom nach
	// CloudEvents-`source` verteilt wird (ADR-034, file-per-partition ADR-037).
	// Default 1 = Single-Instance, verhaltensgleich zur nicht-partitionierten
	// Ablage (CLIO_PARTITIONS; ARCHITECTURE.md §4.1: Skalierung ist opt-in).
	Partitions int

	// PartitionVNodes ist die Anzahl virtueller Knoten je Partition im
	// konsistenten Hash-Ring (CLIO_PARTITION_VNODES). Steuert nur die
	// Gleichverteilung; ohne Wirkung bei Partitions==1. Default 128.
	PartitionVNodes int

	// PartitionsExperimental bestätigt bewusst den Betrieb mit mehr als einer
	// Partition (CLIO_PARTITIONS_EXPERIMENTAL). Die Partitionierung (ADR-034…038) ist
	// erst als Fundament umgesetzt: bei N>1 sind mehrere Pfade bekannt fehlerhaft —
	// Optimistic-Concurrency/Preconditions greifen nur partitionslokal, der
	// State-Cache und die skalaren Lese-Cursor (lowerBound/at) sind bei N>1 nicht
	// wohldefiniert, und Online-Backup/`verify` decken nur eine Partition ab. Ohne
	// dieses ausdrückliche Opt-in verweigert der Start bei Partitions>1 den Dienst,
	// damit N>1 nicht versehentlich produktiv scharf geschaltet wird (WP-4.7).
	PartitionsExperimental bool

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
	// Default 30s (WP-4.3/A4: eine unbegrenzte Query ist eine CPU-/Speicher-DoS-
	// Fläche für read-berechtigte Nutzer). 0 schaltet die Deadline explizit ab —
	// für Spezialfälle mit legitim sehr langen Scans. Der run-query-Stream sendet
	// unabhängig davon einen Heartbeat, der die Proxy-Verbindung offen hält.
	QueryTimeout time.Duration

	// StreamWriteTimeout ist das Fortschritts-Timeout der streamenden Antworten
	// (read-events, observe-events, run-query): vor jedem Write auf die Verbindung
	// wird eine frische Schreib-Deadline gesetzt. Solange der Client Bytes abnimmt,
	// läuft der Stream unbegrenzt weiter (Bulk-Reads/Live-Streams bleiben möglich);
	// ein stehender oder toter Client lässt einen Write in die Deadline laufen —
	// der Handler bricht dann ab und gibt die bbolt-Lesetransaktion frei (WP-4.3/B3,
	// gegen die Read-Tx-/Compaction-Blockade durch langsame Clients). Ersetzt das
	// pauschale, unbegrenzte Aufheben der Server-WriteTimeout auf den Streams. Per
	// CLIO_STREAM_WRITE_TIMEOUT als Go-Dauer; Default 60s (> observe-Heartbeat, damit
	// eine ruhige, aber lebende Verbindung offen bleibt). 0 schaltet das
	// Fortschritts-Timeout ab (unbegrenzt, altes Verhalten).
	StreamWriteTimeout time.Duration

	// PresenceWindow ist das gleitende „Online"-Fenster der Aktivitäts-/Presence-
	// Registry (ADR-030): ein Schlüssel ohne offene Observe-Verbindung gilt als
	// online, solange seine letzte erfolgreiche Aktivität jünger als dieses Fenster
	// ist. Per CLIO_PRESENCE_WINDOW als Go-Dauer (Default 60s). 0 schaltet das
	// zeitbasierte Fenster ab — dann zählt nur eine offene Observe-Verbindung als
	// online.
	PresenceWindow time.Duration

	// AuthEvents schaltet die optionalen Auth-Lifecycle-Events ein (ADR-030):
	// Session-Start/-Ende und Key angelegt/widerrufen werden als CloudEvents in den
	// reservierten, server-only Subject-Namespace /_clio/auth/… geschrieben
	// (Dogfooding — mit run-query/observe/UI abfragbar). Per CLIO_AUTH_EVENTS;
	// Default aus (rückwärtskompatibel, kein Eingriff in den Event-Strom). Bewusst
	// KEIN Event pro Read/Write.
	AuthEvents bool

	// AuthDeniedEvents schaltet zusätzlich access-denied-Events ein (ADR-030):
	// wiederholte 401/403 eines bekannten Schlüssels werden — rate-limitiert gegen
	// Flutung — als Events unter /_clio/auth/denied/… geschrieben. Per
	// CLIO_AUTH_DENIED_EVENTS; Default aus. Greift nur, wenn AuthEvents aktiv ist.
	AuthDeniedEvents bool

	// MCPEnabled mountet den eingebetteten MCP-Server unter POST /mcp (ADR-042):
	// clios Event-Store wird für KI-Agenten als native Tool-Fläche über das Model
	// Context Protocol erreichbar. Per CLIO_MCP; Default aus, damit bestehende
	// Deployments unverändert bleiben. Der MCP-Layer authentifiziert nicht selbst,
	// sondern reicht das Bearer des Aufrufers an die eigene API weiter — es greifen
	// dieselben API-Key-Scopes (ADR-025/033) wie für die REST-Fläche.
	MCPEnabled bool

	// MaxBodyBytes begrenzt die Größe eines Request-Bodys auf den Datenrouten
	// (CLIO_MAX_BODY_BYTES). Ohne Limit kann ein einzelner authentifizierter Client
	// (write-Scope) mit einem sehr großen `events`-Array den Prozess in den OOM
	// treiben — der Body wird beim Dekodieren komplett materialisiert. Das Limit
	// deckelt jeden Request-Body via http.MaxBytesReader; eine Überschreitung ergibt
	// 413. Per CLIO_MAX_BODY_BYTES (Bytes) einstellbar; Default 16 MiB. 0 schaltet
	// das Limit ab (nicht empfohlen; nur für Spezialfälle wie einen vorgelagerten,
	// selbst limitierenden Proxy).
	MaxBodyBytes int64

	// HSTS aktiviert den Strict-Transport-Security-Header (WP-4.5/SEC-M2). Standard
	// aus: clio terminiert kein TLS (läuft hinter einem TLS-Proxy, der HSTS meist
	// selbst setzt); HSTS über eine Klartext-HTTP-Verbindung ist wirkungslos und
	// versehentlich über HTTPS gesetzt kann es Clients bei Fehlkonfiguration
	// aussperren. Per CLIO_HSTS bewusst aktivierbar, wenn clio direkt über HTTPS (via
	// Proxy) erreichbar ist und kein Proxy den Header setzt.
	HSTS bool

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
	envPartition = "CLIO_PARTITIONS"
	envPartVNode = "CLIO_PARTITION_VNODES"
	envPartExp   = "CLIO_PARTITIONS_EXPERIMENTAL"
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
	envPresence  = "CLIO_PRESENCE_WINDOW"
	envAuthEv    = "CLIO_AUTH_EVENTS"
	envAuthDenEv = "CLIO_AUTH_DENIED_EVENTS"
	envMCP       = "CLIO_MCP"
	envMaxBody   = "CLIO_MAX_BODY_BYTES"
	envStreamWTO = "CLIO_STREAM_WRITE_TIMEOUT"
	envHSTS      = "CLIO_HSTS"

	defaultAddr    = ":3000"
	defaultDBPath  = "clio.db"
	defaultSync    = "group"
	defaultObsvPre = 4096 // Anti-Buffering-Polster für observe (siehe Config-Feld)
	maxObsvPre     = 1 << 20

	// maxInitMB deckelt CLIO_DB_INITIAL_MB auf 64 TiB — großzügig genug für jede
	// reale Platte, schützt aber vor versehentlichen Tippfehlern (und Overflow).
	maxInitMB = 64 << 20

	// defaultMaxBodyBytes deckelt Request-Bodies auf 16 MiB — großzügig für normale
	// write-events-Batches, aber ein wirksamer Riegel gegen Speicher-DoS. maxBodyCap
	// begrenzt den Konfigurationswert selbst (Schutz vor Tippfehlern/Overflow).
	defaultMaxBodyBytes = 16 << 20 // 16 MiB
	maxBodyCap          = 4 << 30  // 4 GiB

	// defaultQueryTimeout begrenzt run-query-Scans standardmäßig (WP-4.3/A4). 30s
	// ist großzügig für legitime Abfragen, riegelt aber unbegrenzte CPU-/Speicher-
	// Bindung durch eine einzelne Query ab. 0 (per CLIO_QUERY_TIMEOUT) schaltet ab.
	defaultQueryTimeout = 30 * time.Second

	// defaultStreamWriteTimeout ist das Fortschritts-Timeout je Stream-Write
	// (WP-4.3/B3). 60s liegt über dem observe-Heartbeat (15s), sodass eine ruhige,
	// aber lebende Verbindung durch Heartbeats offen bleibt, ein toter/stehender
	// Client aber zügig freigegeben wird.
	defaultStreamWriteTimeout = 60 * time.Second

	defaultMonInterval    = 60 * time.Second
	defaultPresenceWindow = 60 * time.Second
	defaultGrowPct        = 80
	defaultCompactH       = 6
	maxCompactH           = 168 // eine Woche

	// Partitionierung (ADR-034/037).
	defaultPartitions = 1
	maxPartitions     = 4096 // großzügiger Schutz gegen Tippfehler / FD-Erschöpfung
	defaultVNodes     = 128
	maxVNodes         = 1 << 16
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
		Addr:                   getenvDefault(envAddr, defaultAddr),
		APIToken:               os.Getenv(envToken),
		BootstrapAdminKey:      os.Getenv(envBootstrap),
		DBPath:                 getenvDefault(envDBPath, defaultDBPath),
		Partitions:             parseIntDefault(envPartition, defaultPartitions, 1, maxPartitions),
		PartitionVNodes:        parseIntDefault(envPartVNode, defaultVNodes, 1, maxVNodes),
		PartitionsExperimental: parseBoolDefault(envPartExp, false),
		DBInitialMB:            parseIntDefault(envDBInitMB, 0, 0, maxInitMB),
		DBGrowThresholdPct:     parseIntDefault(envDBGrowPct, defaultGrowPct, 1, 99),
		DBCompactEnabled:       parseBoolDefault(envDBCompact, false),
		DBCompactIntervalH:     parseIntDefault(envDBCompInt, defaultCompactH, 1, maxCompactH),
		Sync:                   getenvDefault(envSync, defaultSync),
		SigningKey:             os.Getenv(envSignKey),
		DevMode:                parseBoolDefault(envDevMode, false),
		Compress:               parseBoolDefault(envCompress, false),
		EventAuthorship:        parseBoolDefault(envEventAuth, false),
		ObservePreambleBytes:   parseIntDefault(envObsvPre, defaultObsvPre, 0, maxObsvPre),
		AuthEvents:             parseBoolDefault(envAuthEv, false),
		AuthDeniedEvents:       parseBoolDefault(envAuthDenEv, false),
		MCPEnabled:             parseBoolDefault(envMCP, false),
		MaxBodyBytes:           parseInt64Default(envMaxBody, defaultMaxBodyBytes, 0, maxBodyCap),
		HSTS:                   parseBoolDefault(envHSTS, false),
	}

	if !validSync[cfg.Sync] {
		return Config{}, fmt.Errorf("%s muss group, always oder off sein, war %q", envSync, cfg.Sync)
	}

	// N>1 ist experimentell (ADR-034…038 sind erst als Fundament umgesetzt): bei
	// mehreren Partitionen sind Preconditions, State-Cache, Lese-Cursor und
	// Backup/verify bekannt unvollständig. Ohne ausdrückliches Opt-in den Start
	// verweigern, damit N>1 nicht versehentlich produktiv läuft (WP-4.7).
	if cfg.Partitions > 1 && !cfg.PartitionsExperimental {
		return Config{}, fmt.Errorf(
			"%s=%d aktiviert die EXPERIMENTELLE Partitionierung (N>1): Preconditions/Optimistic-Concurrency greifen nur partitionslokal, State-Cache und skalare Lese-Cursor (lowerBound/at) sind bei N>1 nicht wohldefiniert, Online-Backup/verify decken nur eine Partition ab. Für Produktion CLIO_PARTITIONS=1 verwenden. Zum bewussten Erproben zusätzlich %s=true setzen",
			envPartition, cfg.Partitions, envPartExp)
	}

	to, err := parseDurationDefault(envQueryTO, defaultQueryTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryTimeout = to

	mon, err := parseDurationDefault(envDBMonInt, defaultMonInterval)
	if err != nil {
		return Config{}, err
	}
	cfg.DBMonitorInterval = mon

	pw, err := parseDurationDefault(envPresence, defaultPresenceWindow)
	if err != nil {
		return Config{}, err
	}
	cfg.PresenceWindow = pw

	swto, err := parseDurationDefault(envStreamWTO, defaultStreamWriteTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.StreamWriteTimeout = swto

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

// parseInt64Default liest eine nicht-negative 64-Bit-Ganzzahl aus der Umgebung und
// begrenzt sie auf [min,max]. Leer oder unlesbar ergibt fallback. Für Byte-Größen
// (z. B. CLIO_MAX_BODY_BYTES), die den int32-Bereich überschreiten können.
func parseInt64Default(key string, fallback, min, max int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
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
