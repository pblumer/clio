// Package partition ist das reine Routing-Domänenmodell der Partitionierung
// (ADR-034, ADR-038): es bildet den aus dem CloudEvents-`source` abgeleiteten
// Stream-/Aggregate-Key über konsistentes Hashing deterministisch auf eine
// Partition ab. Das Paket ist bewusst frei von Storage- und HTTP-Abhängigkeiten
// — es kennt weder bbolt noch net/http — und kommt mit der Standardbibliothek
// aus (ADR-001). Persistenz (internal/store) und Transport (internal/httpapi)
// bauen darauf auf.
//
// Determinismus ist eine harte Anforderung: Dieselbe Eingabe muss über
// Prozess-Neustarts hinweg dieselbe Partition liefern (sonst landen Events
// desselben Aggregats in verschiedenen Ketten). Deshalb wird ausschließlich
// `crypto/sha256` ohne Seed verwendet — kein `maphash` o. Ä.
//
// Skalierung ist opt-in (ARCHITECTURE.md §4.1): Bei einer einzigen Partition
// (N=1, der Default) liefert Partition immer ID(0) — verhaltensgleich zur
// nicht-partitionierten Single-Instance.
package partition

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// ID identifiziert eine Partition. Gültige Werte sind 0..N-1 für einen Ring mit
// N Partitionen.
type ID uint32

// KeyFromSource leitet den Stream-/Aggregate-Key aus dem CloudEvents-`source`
// ab. Events desselben `source` teilen sich denselben Key und damit dieselbe
// Partition — die fachlich tragende Achse (ADR-034): Events eines Aggregats
// werden gemeinsam gelesen und kausal geordnet.
//
// v1 ist die Ableitung die (um umgebenden Whitespace bereinigte) `source`
// selbst. Eine reichere Ableitung (z. B. nur ein Präfix des `source` als
// Aggregate-Wurzel) ist eine bewusst zurückgestellte, additive Erweiterung
// (offener Punkt in ADR-034). Ein leerer `source` ergibt einen leeren Key; die
// Richtlinie dafür (ablehnen / Inbox, vgl. ADR-026) liegt nicht hier, sondern
// im aufrufenden Schreibpfad.
func KeyFromSource(source string) string {
	return strings.TrimSpace(source)
}

// Ring ist ein konsistenter Hash-Ring über die Partitionen 0..N-1. Jede
// Partition wird mit V virtuellen Knoten gleichmäßig auf dem 32-bit-Ring
// platziert, damit die Last bei minimaler Migration bei Ring-Änderung
// ausbalanciert ist (ADR-038). Ein Ring ist nach der Erzeugung unveränderlich
// und damit gefahrlos nebenläufig lesbar.
type Ring struct {
	n      int
	vnodes int
	points []point // nach hash aufsteigend sortiert
}

// point ist ein virtueller Knoten auf dem Ring: die Hash-Position und die
// Partition, zu der er gehört.
type point struct {
	hash uint32
	id   ID
}

// NewRing erzeugt einen Ring mit n Partitionen und v virtuellen Knoten je
// Partition. n und v müssen >= 1 sein. Der Default des Gesamtsystems ist n=1
// (Single-Instance, ADR-034); v steuert nur die Gleichverteilung und hat keinen
// Einfluss auf die Zuordnung bei n=1.
func NewRing(n, v int) (*Ring, error) {
	if n < 1 {
		return nil, fmt.Errorf("partition: Partitionsanzahl muss >= 1 sein, war %d", n)
	}
	if v < 1 {
		return nil, fmt.Errorf("partition: virtuelle Knoten je Partition müssen >= 1 sein, war %d", v)
	}
	r := &Ring{n: n, vnodes: v, points: make([]point, 0, n*v)}
	for id := 0; id < n; id++ {
		for vn := 0; vn < v; vn++ {
			r.points = append(r.points, point{hash: vnodeHash(ID(id), vn), id: ID(id)})
		}
	}
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].hash != r.points[j].hash {
			return r.points[i].hash < r.points[j].hash
		}
		// Stabiler Tie-Break, damit Kollisionen deterministisch aufgelöst werden.
		return r.points[i].id < r.points[j].id
	})
	return r, nil
}

// N liefert die Anzahl der Partitionen des Rings.
func (r *Ring) N() int { return r.n }

// Partition bildet einen Key deterministisch auf eine Partition ab: die erste
// Partition im Uhrzeigersinn ab der Hash-Position des Keys (mit Wraparound).
// Bei N=1 ist das Ergebnis immer ID(0).
func (r *Ring) Partition(key string) ID {
	if r.n == 1 {
		return 0
	}
	h := hash32([]byte(key))
	// Erster Ring-Punkt mit hash >= h; sonst Wraparound auf den ersten Punkt.
	i := sort.Search(len(r.points), func(i int) bool {
		return r.points[i].hash >= h
	})
	if i == len(r.points) {
		i = 0
	}
	return r.points[i].id
}

// PartitionForSource ist die Bequemlichkeitskombination aus KeyFromSource und
// Partition — der übliche Eintrittspunkt im Schreibpfad.
func (r *Ring) PartitionForSource(source string) ID {
	return r.Partition(KeyFromSource(source))
}

// Migration beschreibt, dass ein Key bei einem Ringwechsel von einer Partition
// in eine andere wandert.
type Migration struct {
	Key  string
	From ID
	To   ID
}

// Rebalance liefert für die gegebenen Keys die Wanderungen vom alten zum neuen
// Ring. Es ist ein Diagnose-/Vorschaupfad (v1 ohne Live-Datentransport): er
// belegt die Minimal-Migrations-Eigenschaft des konsistenten Hashings und dient
// als Grundlage für die spätere Rebalancing-Strategie (ADR-038) und das
// Splitting/Merging von Partitionen (offener Punkt in ADR-034). Keys, deren
// Partition unverändert bleibt, erscheinen nicht im Ergebnis.
func Rebalance(old, new *Ring, keys []string) []Migration {
	var moves []Migration
	for _, k := range keys {
		from := old.Partition(k)
		to := new.Partition(k)
		if from != to {
			moves = append(moves, Migration{Key: k, From: from, To: to})
		}
	}
	return moves
}

// hash32 bildet beliebige Bytes auf eine 32-bit-Ringposition ab (die oberen 4
// Bytes des SHA-256). Stabil über Prozess-Neustarts (kein Seed).
func hash32(b []byte) uint32 {
	sum := sha256.Sum256(b)
	return binary.BigEndian.Uint32(sum[:4])
}

// vnodeHash berechnet die Ringposition des vn-ten virtuellen Knotens der
// Partition id. Die Eingabe wird aus festen Big-Endian-Bytes gebildet (kein
// fmt), damit die Position reproduzierbar ist.
func vnodeHash(id ID, vn int) uint32 {
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[0:4], uint32(id))
	binary.BigEndian.PutUint32(buf[4:8], uint32(vn))
	return hash32(buf[:])
}
