package pubsub

import (
	"strconv"
	"testing"

	"github.com/pblumer/clio/internal/event"
)

func ev(id uint64) event.Event {
	return event.Event{ID: strconv.FormatUint(id, 10), Subject: "/a", Type: "t"}
}

func TestPublishDelivers(t *testing.T) {
	b := New()
	sub := b.Subscribe()

	b.Publish([]event.Event{ev(1), ev(2)})

	for i := uint64(1); i <= 2; i++ {
		got := <-sub.Events
		if got.ID != strconv.FormatUint(i, 10) {
			t.Fatalf("event %d: id = %q", i, got.ID)
		}
	}
}

func TestMultipleSubscribersEachReceive(t *testing.T) {
	b := New()
	a := b.Subscribe()
	c := b.Subscribe()

	b.Publish([]event.Event{ev(1)})

	if (<-a.Events).ID != "1" || (<-c.Events).ID != "1" {
		t.Fatal("nicht alle subscriber haben das event erhalten")
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	sub := b.Subscribe()
	if b.SubscriberCount() != 1 {
		t.Fatalf("count = %d, want 1", b.SubscriberCount())
	}
	b.Unsubscribe(sub)
	if b.SubscriberCount() != 0 {
		t.Fatalf("count nach unsubscribe = %d, want 0", b.SubscriberCount())
	}
	// Unsubscribe ist idempotent und Publish ohne Subscriber ist harmlos.
	b.Unsubscribe(sub)
	b.Publish([]event.Event{ev(1)})
}

func TestPublishEmptyNoop(t *testing.T) {
	b := New()
	sub := b.Subscribe()
	b.Publish(nil)
	select {
	case <-sub.Events:
		t.Fatal("leeres publish sollte nichts liefern")
	default:
	}
}

// TestOverflowMarksLost: ein Subscriber, der nicht liest, wird bei Pufferüberlauf
// abgehängt (Lost geschlossen, aus dem Broker entfernt).
func TestOverflowMarksLost(t *testing.T) {
	b := New()
	sub := b.Subscribe()

	overflow := make([]event.Event, defaultBuffer+10)
	for i := range overflow {
		overflow[i] = ev(uint64(i + 1))
	}
	b.Publish(overflow)

	select {
	case <-sub.Lost:
		// erwartet
	default:
		t.Fatal("Lost sollte bei überlauf geschlossen sein")
	}
	if b.SubscriberCount() != 0 {
		t.Fatalf("abgehängter subscriber noch im broker: count = %d", b.SubscriberCount())
	}
}
