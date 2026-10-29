//go:build test

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestNotificationEvents_503WhenHubDisabled(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/events", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.NotificationEvents, req, nil)
	rr.AssertStatusCode(http.StatusServiceUnavailable)
}

func TestNotificationEvents_StreamsConnectedAndPublishes(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()
	hub := services.NewSSEHub()
	handler.SetSSEHub(hub)

	userID := 1
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	t.Cleanup(cancel)
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)
	w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() {
		handler.NotificationEvents(w, req)
		close(done)
	}()

	waitForCond(t, "connected frame", func() bool {
		return strings.Contains(w.String(), "event: connected")
	})
	waitForCond(t, "subscriber registered", func() bool {
		return hub.UserSubscriberCount(userID) == 1
	})
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	hub.PublishUserChange(userID)
	waitForCond(t, "changed frame", func() bool {
		return strings.Contains(w.String(), "event: changed")
	})

	// Another user's invalidation must not reach this stream. Wait out a
	// delivery window before asserting nothing arrived.
	hub.PublishUserChange(userID+1)
	stray := time.NewTicker(20 * time.Millisecond)
	<-stray.C
	stray.Stop()
	if got := strings.Count(w.String(), "event: changed"); got != 1 {
		t.Fatalf("stream received another user's event: %q", w.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
	waitForCond(t, "subscriber cleaned up", func() bool {
		return hub.UserSubscriberCount(userID) == 0
	})
}

func TestNotificationEvents_429WhenUserStreamCapReached(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()
	hub := services.NewSSEHub()
	hub.SetMaxUserStreams(1)
	handler.SetSSEHub(hub)

	release, ok := hub.AcquireUserStream(1)
	if !ok {
		t.Fatal("seed stream acquire failed")
	}
	defer release()

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/events", nil)
	req = testutils.WithAuthContext(req, nil)
	rr := httptest.NewRecorder()
	handler.NotificationEvents(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%q", rr.Code, http.StatusTooManyRequests, rr.Body.String())
	}
	if got := hub.UserSubscriberCount(1); got != 0 {
		t.Fatalf("rejected request registered %d subscribers, want 0", got)
	}
}
