//go:build test

package services

import (
	"testing"
)

func recvKind(t *testing.T, sub *ItemSubscriber) ItemSSEEvent {
	t.Helper()
	select {
	case ev := <-sub.Events():
		return ev
	default:
		t.Fatal("expected an event but channel was empty")
		return ItemSSEEvent{}
	}
}

func TestSSEHub_FanOutToAllSubscribers(t *testing.T) {
	hub := NewSSEHub()
	a := hub.Subscribe(7)
	b := hub.Subscribe(7)
	other := hub.Subscribe(8)

	hub.PublishItemChange(7, ItemChangeComment)

	if got := recvKind(t, a); got.ItemID != 7 || got.Kind != ItemChangeComment {
		t.Errorf("subscriber a got %+v", got)
	}
	if got := recvKind(t, b); got.ItemID != 7 || got.Kind != ItemChangeComment {
		t.Errorf("subscriber b got %+v", got)
	}
	select {
	case ev := <-other.Events():
		t.Errorf("subscriber on item 8 should not receive item 7's event, got %+v", ev)
	default:
	}
}

func TestSSEHub_DropOnFullBufferSetsStale(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.Subscribe(1)

	// Fill the buffer (cap 16) plus one overflow.
	for i := 0; i < 17; i++ {
		hub.PublishItemChange(1, ItemChangeUpdated)
	}

	if !sub.TakeStale() {
		t.Fatal("expected stale flag after buffer overflow")
	}
	// TakeStale clears the flag.
	if sub.TakeStale() {
		t.Fatal("stale flag should clear after TakeStale")
	}
}

func TestSSEHub_UnsubscribeStopsDeliveryAndPrunes(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.Subscribe(42)
	if n := hub.SubscriberCount(42); n != 1 {
		t.Fatalf("expected 1 subscriber, got %d", n)
	}

	hub.Unsubscribe(sub)
	if n := hub.SubscriberCount(42); n != 0 {
		t.Fatalf("expected 0 subscribers after unsubscribe, got %d", n)
	}

	// Publishing to a pruned topic must not panic and delivers nothing.
	hub.PublishItemChange(42, ItemChangeUpdated)
	select {
	case ev := <-sub.Events():
		t.Errorf("unsubscribed subscriber received %+v", ev)
	default:
	}
}

func TestSSEHub_ImplementsPublisherForMutations(t *testing.T) {
	// Installing the hub as the process publisher (as server.go does) routes
	// PublishItemChange — called by every mutation chokepoint — to subscribers.
	hub := NewSSEHub()
	SetItemChangePublisher(hub)
	t.Cleanup(func() { SetItemChangePublisher(nil) })

	sub := hub.Subscribe(99)
	PublishItemChange(99, ItemChangeStatus)

	if got := recvKind(t, sub); got.Kind != ItemChangeStatus {
		t.Errorf("expected status event via package publisher, got %+v", got)
	}
}
