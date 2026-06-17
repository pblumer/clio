package query

import (
	"fmt"
	"testing"
)

// TestCompileCacheHitReusesPredicate stellt sicher, dass derselbe Ausdruck ein
// wiederverwendbares (identisches) Predicate aus dem Cache liefert.
func TestCompileCacheHitReusesPredicate(t *testing.T) {
	c := mustCompiler(t)
	const src = "event.type == 'order-placed'"
	p1, err := c.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	p2, err := c.Compile(src)
	if err != nil {
		t.Fatalf("compile (2): %v", err)
	}
	if p1 != p2 {
		t.Fatal("cache-hit sollte dieselbe Predicate-Instanz liefern")
	}
	if len(c.cache) != 1 || len(c.order) != 1 {
		t.Fatalf("cache/order sollten genau 1 Eintrag haben, sind %d/%d", len(c.cache), len(c.order))
	}
}

// TestCompileCacheDoesNotExceedMax füllt den Cache über seine Grenze hinaus und
// prüft, dass er die Maximalgröße nie überschreitet (cache und Einfüge-
// reihenfolge bleiben synchron).
func TestCompileCacheDoesNotExceedMax(t *testing.T) {
	c := mustCompiler(t)
	c.maxCache = 4

	for i := 0; i < 50; i++ {
		if _, err := c.Compile(fmt.Sprintf("event.type == 't%d'", i)); err != nil {
			t.Fatalf("compile t%d: %v", i, err)
		}
		if len(c.cache) > c.maxCache {
			t.Fatalf("cache überschreitet maximum: %d > %d", len(c.cache), c.maxCache)
		}
		if len(c.cache) != len(c.order) {
			t.Fatalf("cache (%d) und order (%d) laufen auseinander", len(c.cache), len(c.order))
		}
	}
	if len(c.cache) != c.maxCache {
		t.Fatalf("cache sollte voll sein (%d), ist %d", c.maxCache, len(c.cache))
	}
}

// TestCompileCacheEvictsFIFO prüft die deterministische FIFO-Eviction: der
// jeweils älteste Eintrag wird verdrängt, neuere bleiben erhalten.
func TestCompileCacheEvictsFIFO(t *testing.T) {
	c := mustCompiler(t)
	c.maxCache = 3

	// t0, t1, t2 füllen den Cache exakt aus.
	p0, _ := c.Compile("event.type == 't0'")
	c.Compile("event.type == 't1'")
	c.Compile("event.type == 't2'")
	if _, ok := c.cache["event.type == 't0'"]; !ok {
		t.Fatal("t0 sollte vor der Eviction im Cache sein")
	}

	// t3 verdrängt den ältesten Eintrag (t0); t1..t3 bleiben.
	c.Compile("event.type == 't3'")
	if _, ok := c.cache["event.type == 't0'"]; ok {
		t.Fatal("t0 hätte als ältester Eintrag verdrängt werden müssen")
	}
	for _, expr := range []string{"event.type == 't1'", "event.type == 't2'", "event.type == 't3'"} {
		if _, ok := c.cache[expr]; !ok {
			t.Fatalf("%q sollte noch im Cache sein", expr)
		}
	}

	// Ein verdrängter Ausdruck wird neu kompiliert — neue, eigenständige Instanz.
	p0again, err := c.Compile("event.type == 't0'")
	if err != nil {
		t.Fatalf("recompile t0: %v", err)
	}
	if p0again == p0 {
		t.Fatal("nach Eviction sollte t0 eine neue Predicate-Instanz ergeben")
	}
}
