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

// Histogram zählt geschriebene Events in Zeit-Buckets ab origin. Alle Methoden
// sind nebenläufig sicher.
type Histogram struct {
	mu         sync.Mutex
	origin     time.Time     // Beginn von Bucket 0 (= Serverstart)
	width      time.Duration // aktuelle Bucket-Breite (≥ 1s, verdoppelt sich)
	counts     []uint64      // counts[i] = Events in [origin+i*width, origin+(i+1)*width)
	total      uint64
	maxBuckets int
}

// New erzeugt ein leeres Histogramm mit Startzeitpunkt start.
func New(start time.Time) *Histogram {
	return &Histogram{origin: start.UTC(), width: time.Second, maxBuckets: defaultMaxBuckets}
}

// Add verbucht n Events zum Zeitpunkt at. n ≤ 0 ist ein No-op.
func (h *Histogram) Add(n int, at time.Time) {
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
}

// compact halbiert die Auflösung: je zwei benachbarte Buckets werden summiert,
// die Breite verdoppelt. Caller hält den Lock.
func (h *Histogram) compact() {
	merged := make([]uint64, (len(h.counts)+1)/2)
	for i, c := range h.counts {
		merged[i/2] += c
	}
	h.counts = merged
	h.width *= 2
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
