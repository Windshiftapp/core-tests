//go:build test

package services

import (
	"testing"
	"time"
)

func receiveWorkspaceEvent(t *testing.T, sub *SSESubscriber[WorkspaceSSEEvent]) WorkspaceSSEEvent {
	t.Helper()
	select {
	case ev := <-sub.Events():
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for workspace event")
		return WorkspaceSSEEvent{}
	}
}

func TestSSEHubWorkspaceFanOut(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeWorkspaces([]int{1, 2})
	defer hub.UnsubscribeWorkspaces(sub)

	if got := hub.WorkspaceSubscriberCount(1); got != 1 {
		t.Fatalf("workspace 1 subscribers = %d, want 1", got)
	}
	if got := hub.WorkspaceSubscriberCount(2); got != 1 {
		t.Fatalf("workspace 2 subscribers = %d, want 1", got)
	}

	hub.PublishWorkspaceChange(2)

	ev := receiveWorkspaceEvent(t, sub)
	if ev.WorkspaceID != 2 {
		t.Fatalf("event = %+v, want workspace 2 items", ev)
	}

	// A publish to a workspace the subscriber did not register for is dropped.
	hub.PublishWorkspaceChange(3)
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event for unsubscribed workspace: %+v", ev)
	default:
	}
}

func TestSSEHubWorkspaceOverflowMarksStale(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeWorkspaces([]int{1})
	defer hub.UnsubscribeWorkspaces(sub)

	// The channel buffer is 16; the 17th publish must drop and flag stale.
	for i := 0; i < 17; i++ {
		hub.PublishWorkspaceChange(1)
	}

	if !sub.TakeStale() {
		t.Fatal("expected stale after buffer overflow")
	}
	if sub.TakeStale() {
		t.Fatal("stale flag should be cleared after TakeStale")
	}
}

func TestSSEHubWorkspaceUnsubscribePrunesTopics(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeWorkspaces([]int{1, 2})
	hub.UnsubscribeWorkspaces(sub)

	if got := hub.WorkspaceSubscriberCount(1); got != 0 {
		t.Fatalf("workspace 1 subscribers = %d, want 0", got)
	}
	if got := hub.WorkspaceSubscriberCount(2); got != 0 {
		t.Fatalf("workspace 2 subscribers = %d, want 0", got)
	}
}

func TestSSEHubSubscribeWorkspacesIgnoresDuplicatesAndNonPositive(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeWorkspaces([]int{5, 5, 0, -1})
	defer hub.UnsubscribeWorkspaces(sub)

	if got := hub.WorkspaceSubscriberCount(5); got != 1 {
		t.Fatalf("workspace 5 subscribers = %d, want 1", got)
	}
	if got := hub.WorkspaceSubscriberCount(0); got != 0 {
		t.Fatalf("workspace 0 subscribers = %d, want 0", got)
	}
}

func TestSSEHubUserFanOut(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeUser(11)
	defer hub.UnsubscribeUser(sub)

	if got := hub.UserSubscriberCount(11); got != 1 {
		t.Fatalf("user 11 subscribers = %d, want 1", got)
	}

	hub.PublishUserChange(11)
	select {
	case ev := <-sub.Events():
		if ev.UserID != 11 {
			t.Fatalf("event = %+v, want user 11 notifications", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user event")
	}

	hub.PublishUserChange(12)
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event for another user: %+v", ev)
	default:
	}
}

func TestSSEHubUserOverflowMarksStale(t *testing.T) {
	hub := NewSSEHub()
	sub := hub.SubscribeUser(11)
	defer hub.UnsubscribeUser(sub)

	for i := 0; i < 17; i++ {
		hub.PublishUserChange(11)
	}
	if !sub.TakeStale() {
		t.Fatal("expected stale after buffer overflow")
	}
}

func TestPublishUserChangeRoutesToInstalledPublisher(t *testing.T) {
	hub := NewSSEHub()
	SetUserChangePublisher(hub)
	t.Cleanup(func() { SetUserChangePublisher(nil) })

	sub := hub.SubscribeUser(3)
	defer hub.UnsubscribeUser(sub)

	PublishUserChange(3)
	select {
	case ev := <-sub.Events():
		if ev.UserID != 3 {
			t.Fatalf("event = %+v, want user 3 notifications", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user event")
	}
}

func TestPublishWorkspaceChangeRoutesToInstalledPublisher(t *testing.T) {
	hub := NewSSEHub()
	SetWorkspaceChangePublisher(hub)
	t.Cleanup(func() { SetWorkspaceChangePublisher(nil) })

	sub := hub.SubscribeWorkspaces([]int{7})
	defer hub.UnsubscribeWorkspaces(sub)

	PublishWorkspaceChange(7)

	ev := receiveWorkspaceEvent(t, sub)
	if ev.WorkspaceID != 7 {
		t.Fatalf("event = %+v, want workspace 7 items", ev)
	}

	// A non-positive workspace id is ignored.
	PublishWorkspaceChange(0)
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event for workspace 0: %+v", ev)
	default:
	}
}

func TestSSEHubCapsPerUserStreams(t *testing.T) {
	hub := NewSSEHub()
	hub.SetMaxUserStreams(2)

	first, ok := hub.AcquireUserStream(7)
	if !ok {
		t.Fatal("first stream acquire failed")
	}
	second, ok := hub.AcquireUserStream(7)
	if !ok {
		t.Fatal("second stream acquire failed")
	}
	if _, ok := hub.AcquireUserStream(7); ok {
		t.Fatal("third stream acquired beyond the per-user cap")
	}

	// The cap is per user: another user is unaffected.
	other, ok := hub.AcquireUserStream(8)
	if !ok {
		t.Fatal("second user blocked by another user's cap")
	}
	other()

	// Releasing a slot lets the user open another stream.
	first()
	if _, ok := hub.AcquireUserStream(7); !ok {
		t.Fatal("slot was not freed after release")
	}

	// A double release must not underflow the counter.
	first()
	if got := hub.UserStreamCount(7); got != 2 {
		t.Fatalf("user stream count = %d, want 2", got)
	}
	second()
}

func TestSSEHubDefaultUserStreamCap(t *testing.T) {
	hub := NewSSEHub()
	for i := 0; i < DefaultMaxUserStreams; i++ {
		if _, ok := hub.AcquireUserStream(7); !ok {
			t.Fatalf("acquire %d within the default cap failed", i+1)
		}
	}
	if _, ok := hub.AcquireUserStream(7); ok {
		t.Fatalf("acquired beyond the default cap of %d", DefaultMaxUserStreams)
	}
}
