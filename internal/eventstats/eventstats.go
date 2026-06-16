// Package eventstats führt ein schlankes, abhängigkeitsfreies In-Memory-
// Histogramm der Events über die Zeit (Buckets ab origin). Es erlaubt dem
// /ui-Dashboard, die Eventmengen über die Zeitachse darzustellen, ohne die
// gesamte Historie zum Client zu streamen. Der Server baut es beim Start aus der
// vorhandenen Historie auf (origin = erste Eventzeit) und schreibt es bei jedem
// Write fort.
//
// Die Bucket-Breite ist adaptiv: Sie beginnt fein (eine Sekunde) und verdoppelt
// sich, sobald die Bucketzahl eine Obergrenze überschreitet (paarweises Mergen).
// So bleibt der Speicher- und Antwort-Aufwand konstant beschränkt — passend zum
// Ziel „schlankes Single-Binary, eigene Metriken" (ADR-013), kein Prometheus-
// Client, keine Fremd-Abhängigkeit.
package eventstats

import (
	"sync"
	"time"
)

// defaultMaxBuckets begrenzt die Bucketzahl (und damit die Antwortgröße). Bei
// Überschreitung wird die Bucket-Breite verdoppelt.
const defaultMaxBuckets = 600

// defaultMaxSources begrenzt die Zahl getrennt geführter Source-Serien (Schutz
// gegen unbegrenzten Speicher bei hoher Source-Kardinalität). Treten mehr
// verschiedene Sources auf, landen weitere im Overflow-Schlüssel (OverflowSource).
const defaultMaxSources = 64

// OverflowSource ist der Serien-Schlüssel, in dem Events gezählt werden, sobald
// das Tracking-Limit (maxSources) erreicht ist. Das UI labelt diese Serie als
// „andere". Der Sentinel-Wert enthält ein NUL-Byte, das in CloudEvents-Sources
// nicht vorkommt — so kollidiert er nie mit einer echten Source.
const OverflowSource = "\x00overflow"

// Histogram zählt geschriebene Events in Zeit-Buckets ab origin, optional getrennt
// nach CloudEvents-`source`. Alle Methoden sind nebenläufig sicher.
type Histogram struct {
	mu     sync.Mutex
	origin time.Time     // Beginn von Bucket 0 (= Serverstart)
	width  time.Duration // aktuelle Bucket-Breite (≥ 1s, verdoppelt sich)
	counts []uint64      // counts[i] = Events in [origin+i*width, origin+(i+1)*width)
	total  uint64
	// series[source][i] zählt Events der Source im selben Bucket i wie counts.
	// Eine Serie darf kürzer als counts sein (fehlende Bucket = 0).
	series     map[string][]uint64
	maxBuckets int
	maxSources int
}

// New erzeugt ein leeres Histogramm mit Startzeitpunkt start.
func New(start time.Time) *Histogram {
	return &Histogram{
		origin:     start.UTC(),
		width:      time.Second,
		series:     map[string][]uint64{},
		maxBuckets: defaultMaxBuckets,
		maxSources: defaultMaxSources,
	}
}

// Add verbucht n Events zum Zeitpunkt at (ohne Source-Aufschlüsselung). n ≤ 0 ist
// ein No-op.
func (h *Histogram) Add(n int, at time.Time) { h.add(n, at, "", false) }

// AddSource verbucht n Events der angegebenen `source` zum Zeitpunkt at — sowohl
// im Gesamthistogramm als auch in der Serie der Source. n ≤ 0 ist ein No-op.
func (h *Histogram) AddSource(n int, at time.Time, source string) { h.add(n, at, source, true) }

func (h *Histogram) add(n int, at time.Time, source string, withSource bool) {
	if n <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	at = at.UTC()
	if at.Before(h.origin) {
		at = h.origin
	}
	idx := int(at.Sub(h.origin) / h.width)
	// Bei Überlauf: Bucket-Breite verdoppeln und Paare mergen, bis idx passt.
	for idx >= h.maxBuckets {
		h.compact()
		idx /= 2
	}
	for idx >= len(h.counts) {
		h.counts = append(h.counts, 0)
	}
	h.counts[idx] += uint64(n)
	h.total += uint64(n)

	if withSource {
		key := h.sourceKey(source)
		s := h.series[key]
		for idx >= len(s) {
			s = append(s, 0)
		}
		s[idx] += uint64(n)
		h.series[key] = s
	}
}

// sourceKey liefert den Serien-Schlüssel für source: die Source selbst, solange
// sie bereits geführt wird oder das Limit nicht erreicht ist, sonst den
// Overflow-Schlüssel. Caller hält den Lock.
func (h *Histogram) sourceKey(source string) string {
	if _, ok := h.series[source]; ok {
		return source
	}
	if len(h.series) < h.maxSources {
		return source
	}
	return OverflowSource
}

// Reset leert das Histogramm und setzt den Startzeitpunkt (origin) neu. Genutzt
// vom Dev-Mode-DB-Reset (ADR-022), damit der Eventstrom-Chart nach dem Tabula
// rasa ebenfalls bei null beginnt. Nebenläufig sicher.
func (h *Histogram) Reset(start time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.origin = start.UTC()
	h.width = time.Second
	h.counts = nil
	h.total = 0
	h.series = map[string][]uint64{}
}

// compact halbiert die Auflösung: je zwei benachbarte Buckets werden summiert,
// die Breite verdoppelt — für das Gesamthistogramm und jede Source-Serie in
// gleicher Weise, damit alle Serien zu counts ausgerichtet bleiben. Caller hält
// den Lock.
func (h *Histogram) compact() {
	h.counts = mergePairs(h.counts)
	for k, s := range h.series {
		h.series[k] = mergePairs(s)
	}
	h.width *= 2
}

// mergePairs summiert je zwei benachbarte Buckets (i und i+1 → i/2).
func mergePairs(c []uint64) []uint64 {
	merged := make([]uint64, (len(c)+1)/2)
	for i, v := range c {
		merged[i/2] += v
	}
	return merged
}

// Snapshot ist eine kopierte Momentaufnahme des Histogramms.
type Snapshot struct {
	Origin time.Time
	Width  time.Duration
	Counts []uint64
	Total  uint64
}

// Snapshot liefert eine konsistente Kopie. Die Counts werden kopiert, damit der
// Aufrufer sie gefahrlos serialisieren kann.
func (h *Histogram) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts := make([]uint64, len(h.counts))
	copy(counts, h.counts)
	return Snapshot{Origin: h.origin, Width: h.width, Counts: counts, Total: h.total}
}

// SourceSnapshot ist eine kopierte Momentaufnahme inkl. Aufschlüsselung nach
// Source. Sources[key][i] ist zu Counts[i] ausgerichtet (gleicher Zeit-Bucket);
// eine Serie kann kürzer als Counts sein (fehlende Buckets = 0). Der Schlüssel
// OverflowSource sammelt die Sources jenseits des Tracking-Limits.
type SourceSnapshot struct {
	Origin  time.Time
	Width   time.Duration
	Total   uint64
	Counts  []uint64
	Sources map[string][]uint64
}

// SnapshotBySource liefert eine konsistente, kopierte Momentaufnahme mit der
// Aufschlüsselung nach Source.
func (h *Histogram) SnapshotBySource() SourceSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts := append([]uint64(nil), h.counts...)
	sources := make(map[string][]uint64, len(h.series))
	for k, s := range h.series {
		sources[k] = append([]uint64(nil), s...)
	}
	return SourceSnapshot{Origin: h.origin, Width: h.width, Total: h.total, Counts: counts, Sources: sources}
}
