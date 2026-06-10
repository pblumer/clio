// Package pubsub stellt einen einfachen In-Memory-Broker bereit, über den
// geschriebene Events an offene Observe-Verbindungen verteilt werden (Stufe 2).
//
// Der Broker ist bewusst minimal: jeder Subscriber erhält einen gepufferten
// Kanal. Kann ein langsamer Subscriber nicht Schritt halten und läuft sein
// Puffer über, wird er als „abgehängt" markiert (Lost-Signal) statt den
// Schreibpfad zu blockieren. Der Client erkennt das und verbindet sich mit
// lowerBound neu, um die verpassten Events nachzuladen.
package pubsub

import (
	"sync"

	"github.com/pblumer/clio/internal/event"
)

// defaultBuffer ist die Kanal-Puffergröße pro Subscriber.
const defaultBuffer = 256

// Subscription ist das Abonnement einer Observe-Verbindung.
type Subscription struct {
	// Events liefert neu geschriebene Events in globaler Reihenfolge.
	Events chan event.Event
	// Lost wird geschlossen, wenn Events verworfen wurden, weil der
	// Subscriber nicht Schritt gehalten hat. Der Consumer sollte dann
	// abbrechen und neu verbinden.
	Lost chan struct{}

	lost bool
}

// Broker verteilt Events an alle aktiven Subscriptions.
type Broker struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

// New erstellt einen leeren Broker.
func New() *Broker {
	return &Broker{subs: make(map[*Subscription]struct{})}
}

// Subscribe registriert eine neue Subscription.
func (b *Broker) Subscribe() *Subscription {
	sub := &Subscription{
		Events: make(chan event.Event, defaultBuffer),
		Lost:   make(chan struct{}),
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

// Unsubscribe entfernt eine Subscription. Idempotent.
func (b *Broker) Unsubscribe(sub *Subscription) {
	b.mu.Lock()
	delete(b.subs, sub)
	b.mu.Unlock()
}

// Publish verteilt die Events an alle Subscriptions. Subscriber, deren Puffer
// voll ist, werden abgehängt (Lost geschlossen, aus dem Broker entfernt) — der
// Schreibpfad blockiert dadurch nie.
func (b *Broker) Publish(events []event.Event) {
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	for sub := range b.subs {
		for _, ev := range events {
			select {
			case sub.Events <- ev:
			default:
				// Puffer voll: Subscriber abhängen.
				if !sub.lost {
					sub.lost = true
					close(sub.Lost)
				}
				delete(b.subs, sub)
			}
			if sub.lost {
				break
			}
		}
	}
}

// SubscriberCount liefert die Anzahl aktiver Subscriptions (für Tests/Metrics).
func (b *Broker) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
