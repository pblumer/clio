// Package metrics stellt einen schlanken, abhängigkeitsfreien Metrik-Sammler
// im Prometheus-Textformat bereit (kein Prometheus-Client nötig).
package metrics

import (
	"fmt"
	"io"
	"runtime"
	rtm "runtime/metrics"
	"sort"
	"strconv"
	"sync"
	"time"
)

// durationBuckets sind die (kumulativen) le-Grenzen des Latenz-Histogramms.
var durationBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
}

type reqKey struct {
	method string
	route  string
	status int
}

// authKey identifiziert eine Autorisierungsentscheidung nach Scope und Ausgang
// (allow/deny) — die Basis des clio_auth_decisions_total-Counters (ADR-030).
type authKey struct {
	scope    string
	decision string
}

// Gauges sind Momentaufnahmen, die beim Rendern von außen geliefert werden.
type Gauges struct {
	ActiveObservers int
	OnlineKeys      int // aktuell als online geltende Schlüssel (ADR-030)
	EventsTotal     uint64
	DBSizeBytes     int64
	DBDataBytes     int64 // genutzter Umfang (High-Water-Mark; < 0 = unbekannt)
	DBInitialBytes  int64 // vorbelegte Grenze CLIO_DB_INITIAL_MB (< 0 = aus/unbekannt)
	DBUsedBytes     int64 // belegter Anteil der DB-Datei (< 0 = unbekannt)
	DBFreeBytes     int64 // wiederverwendbarer freier Anteil (< 0 = unbekannt)
	DiskFreeBytes   int64 // freier Speicher auf dem Dateisystem der DB (< 0 = unbekannt)
	DiskTotalBytes  int64 // Gesamtspeicher auf dem Dateisystem der DB (< 0 = unbekannt)
}

// Metrics sammelt HTTP- und Domänen-Metriken. Alle Methoden sind nebenläufig
// sicher.
type Metrics struct {
	mu                   sync.Mutex
	requests             map[reqKey]uint64
	bucket               []uint64 // kumulativ, parallel zu durationBuckets
	durSum               float64
	durCount             uint64
	eventsWritten        uint64
	preconditionFailures uint64
	authDecisions        map[authKey]uint64
}

// New erstellt einen leeren Sammler.
func New() *Metrics {
	return &Metrics{
		requests:      make(map[reqKey]uint64),
		bucket:        make([]uint64, len(durationBuckets)),
		authDecisions: make(map[authKey]uint64),
	}
}

// ObserveRequest verbucht eine abgeschlossene HTTP-Anfrage.
func (m *Metrics) ObserveRequest(method, route string, status int, d time.Duration) {
	secs := d.Seconds()
	m.mu.Lock()
	m.requests[reqKey{method, route, status}]++
	m.durSum += secs
	m.durCount++
	for i, b := range durationBuckets {
		if secs <= b {
			m.bucket[i]++
		}
	}
	m.mu.Unlock()
}

// ObserveAuthDecision verbucht eine Autorisierungsentscheidung (ADR-030):
// scope ist der geforderte Scope der Route, decision "allow" oder "deny".
func (m *Metrics) ObserveAuthDecision(scope, decision string) {
	m.mu.Lock()
	m.authDecisions[authKey{scope, decision}]++
	m.mu.Unlock()
}

// AddEventsWritten zählt erfolgreich geschriebene Events.
func (m *Metrics) AddEventsWritten(n int) {
	m.mu.Lock()
	m.eventsWritten += uint64(n)
	m.mu.Unlock()
}

// IncPreconditionFailure zählt eine fehlgeschlagene Precondition (HTTP 409).
func (m *Metrics) IncPreconditionFailure() {
	m.mu.Lock()
	m.preconditionFailures++
	m.mu.Unlock()
}

// Write rendert alle Metriken im Prometheus-Textformat.
func (m *Metrics) Write(w io.Writer, g Gauges) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprintln(w, "# HELP clio_http_requests_total Anzahl der HTTP-Anfragen.")
	fmt.Fprintln(w, "# TYPE clio_http_requests_total counter")
	keys := make([]reqKey, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})
	for _, k := range keys {
		fmt.Fprintf(w, "clio_http_requests_total{method=%q,route=%q,status=\"%d\"} %d\n",
			k.method, k.route, k.status, m.requests[k])
	}

	fmt.Fprintln(w, "# HELP clio_http_request_duration_seconds Antwortzeit der HTTP-Anfragen.")
	fmt.Fprintln(w, "# TYPE clio_http_request_duration_seconds histogram")
	for i, b := range durationBuckets {
		fmt.Fprintf(w, "clio_http_request_duration_seconds_bucket{le=%q} %d\n", formatFloat(b), m.bucket[i])
	}
	fmt.Fprintf(w, "clio_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.durCount)
	fmt.Fprintf(w, "clio_http_request_duration_seconds_sum %s\n", formatFloat(m.durSum))
	fmt.Fprintf(w, "clio_http_request_duration_seconds_count %d\n", m.durCount)

	writeCounter(w, "clio_events_written_total", "Anzahl geschriebener Events.", m.eventsWritten)
	writeCounter(w, "clio_precondition_failures_total", "Fehlgeschlagene Preconditions (HTTP 409).", m.preconditionFailures)

	if len(m.authDecisions) > 0 {
		fmt.Fprintln(w, "# HELP clio_auth_decisions_total Autorisierungsentscheidungen nach Scope und Ausgang (ADR-030).")
		fmt.Fprintln(w, "# TYPE clio_auth_decisions_total counter")
		akeys := make([]authKey, 0, len(m.authDecisions))
		for k := range m.authDecisions {
			akeys = append(akeys, k)
		}
		sort.Slice(akeys, func(i, j int) bool {
			if akeys[i].scope != akeys[j].scope {
				return akeys[i].scope < akeys[j].scope
			}
			return akeys[i].decision < akeys[j].decision
		})
		for _, k := range akeys {
			fmt.Fprintf(w, "clio_auth_decisions_total{scope=%q,decision=%q} %d\n", k.scope, k.decision, m.authDecisions[k])
		}
	}

	writeGauge(w, "clio_active_observers", "Aktuell offene observe-Verbindungen.", uint64(g.ActiveObservers))
	writeGauge(w, "clio_online_keys", "Aktuell als online geltende Schlüssel (ADR-030).", uint64(g.OnlineKeys))
	writeGauge(w, "clio_events_total", "Anzahl gespeicherter Events.", g.EventsTotal)
	if g.DBSizeBytes >= 0 {
		writeGauge(w, "clio_db_size_bytes", "Größe der Datenbankdatei in Bytes.", uint64(g.DBSizeBytes))
	}
	if g.DBDataBytes >= 0 {
		writeGauge(w, "clio_db_data_bytes", "Tatsächlich genutzter Umfang der DB in Bytes (High-Water-Mark); bei vorbelegter Datei kleiner als clio_db_size_bytes.", uint64(g.DBDataBytes))
	}
	if g.DBInitialBytes > 0 {
		writeGauge(w, "clio_db_initial_bytes", "Vorbelegte DB-Grenze in Bytes (CLIO_DB_INITIAL_MB); ab Annäherung drohen bbolt-Remap-Latenzspitzen.", uint64(g.DBInitialBytes))
	}
	if g.DBUsedBytes >= 0 {
		writeGauge(w, "clio_db_used_bytes", "Belegter Anteil der Datenbankdatei in Bytes (Nutzdaten + Strukturen).", uint64(g.DBUsedBytes))
	}
	if g.DBFreeBytes >= 0 {
		writeGauge(w, "clio_db_free_bytes", "Freier, wiederverwendbarer Anteil der Datenbankdatei in Bytes (per compact rückgewinnbar).", uint64(g.DBFreeBytes))
	}
	if g.DiskFreeBytes >= 0 {
		writeGauge(w, "clio_disk_free_bytes", "Freier Speicher auf dem Dateisystem der Datenbankdatei.", uint64(g.DiskFreeBytes))
	}
	if g.DiskTotalBytes >= 0 {
		writeGauge(w, "clio_disk_total_bytes", "Gesamtspeicher auf dem Dateisystem der Datenbankdatei.", uint64(g.DiskTotalBytes))
	}

	writeRuntime(w)
}

// writeRuntime ergänzt Laufzeit-Metriken (Speicher, Goroutinen, CPU) aus der
// Standardbibliothek — ohne Stop-the-World (runtime/metrics) bzw. via getrusage.
func writeRuntime(w io.Writer) {
	samples := []rtm.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/sched/goroutines:goroutines"},
	}
	rtm.Read(samples)
	writeGauge(w, "clio_memory_heap_bytes", "Live-Heap-Objekte in Bytes.", samples[0].Value.Uint64())
	writeGauge(w, "clio_memory_sys_bytes", "Vom Laufzeitsystem reservierter Speicher in Bytes.", samples[1].Value.Uint64())
	writeGauge(w, "clio_goroutines", "Aktuelle Anzahl Goroutinen.", samples[2].Value.Uint64())
	writeGauge(w, "clio_num_cpu", "Anzahl logischer CPUs.", uint64(runtime.NumCPU()))

	// Prozess-CPU (user+sys) als Counter; der Client bildet daraus die
	// Auslastung. Auf Plattformen ohne getrusage entfällt die Serie.
	if cpu, ok := processCPUSeconds(); ok {
		fmt.Fprintf(w, "# HELP clio_process_cpu_seconds_total Verbrauchte CPU-Zeit (user+sys) in Sekunden.\n")
		fmt.Fprintf(w, "# TYPE clio_process_cpu_seconds_total counter\n")
		fmt.Fprintf(w, "clio_process_cpu_seconds_total %s\n", formatFloat(cpu))
	}
}

func writeCounter(w io.Writer, name, help string, v uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
}

func writeGauge(w io.Writer, name, help string, v uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
