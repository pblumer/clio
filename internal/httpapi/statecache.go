package httpapi

import (
	"container/list"
	"sync"
)

// defaultStateCacheSize begrenzt, wie viele Subject-Zustände der In-Memory-Cache
// hält (ADR-040). Der Cache ist ephemer und size-bound; bei Überlauf wird der am
// längsten nicht genutzte Eintrag verworfen (LRU).
const defaultStateCacheSize = 2048

// stateEntry ist ein gecachter, gefalteter Subject-Zustand. fingerprint bindet den
// Stand an die wirksame Reduce-Spec (ADR-041): ändert sich die Spec, passt der
// Fingerprint nicht mehr und der Stand wird neu gefaltet. lastID ist die höchste
// bereits eingefaltete Event-ID — von hier wird lazy-inkrementell weitergefaltet
// (Append-only-Garantie, ADR-006/015, macht das korrekt).
type stateEntry struct {
	fingerprint   string
	state         map[string]any
	count         uint64
	firstID       string
	lastID        string
	lastSeq       uint64
	lastEventType string
	lastEventTime string
}

// stateCache ist ein thread-sicherer LRU-Cache gefalteter Subject-Zustände
// (ADR-040). Schlüssel ist das Subject; jeder Eintrag trägt den Spec-Fingerprint,
// unter dem er gefaltet wurde.
type stateCache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List               // MRU vorne
	items map[string]*list.Element // subject → Listenelement (value: *cacheNode)
}

type cacheNode struct {
	subject string
	entry   stateEntry
}

func newStateCache(capacity int) *stateCache {
	if capacity <= 0 {
		capacity = defaultStateCacheSize
	}
	return &stateCache{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

// get liefert eine Kopie des Eintrags für subject (und markiert ihn als zuletzt
// genutzt). ok=false, wenn kein Eintrag existiert.
func (c *stateCache) get(subject string) (stateEntry, bool) {
	if c == nil {
		return stateEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[subject]
	if !ok {
		return stateEntry{}, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cacheNode).entry, true
}

// put speichert/aktualisiert den Eintrag für subject und verdrängt bei Überlauf
// den am längsten ungenutzten Eintrag.
func (c *stateCache) put(subject string, entry stateEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[subject]; ok {
		el.Value.(*cacheNode).entry = entry
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cacheNode{subject: subject, entry: entry})
	c.items[subject] = el
	if c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheNode).subject)
		}
	}
}

// clear leert den Cache vollständig (z. B. nach einem Dev-Reset, ADR-022).
func (c *stateCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element, c.cap)
}
