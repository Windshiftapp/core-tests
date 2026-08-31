//go:build test

package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/services"
	"windshift/internal/testutils"
)

// syncResponseWriter is a thread-safe ResponseWriter+Flusher so the test can
// read the streamed output while the blocking SSE handler writes from another
// goroutine.
type syncResponseWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	hdr    http.Header
	writes chan struct{}
}

func (w *syncResponseWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *syncResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.writes <- struct{}{}:
	default:
	}
	return n, err
}

func (w *syncResponseWriter) WriteHeader(int) {}
func (w *syncResponseWriter) Flush()          {}

func (w *syncResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *syncResponseWriter) waitForText(t *testing.T, text string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(w.String(), text) {
			return
		}
		select {
		case <-w.writes:
		case <-deadline.C:
			t.Fatalf("timed out waiting for streamed text %q", text)
		}
	}
}

func newItemHandlerWithHub(t *testing.T, tdb *testutils.TestDB, hub *services.SSEHub) *ItemHandler {
	t.Helper()
	permService, actTracker, notifService := createTestServices(t, *tdb)
	h := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	if hub != nil {
		h.SetSSEHub(hub)
	}
	return h
}

func TestItemEvents_503WhenHubDisabled(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	itemID := createTestItemForComments(t, tdb, data)

	h := newItemHandlerWithHub(t, tdb, nil) // no hub wired

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/events", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	rr := testutils.ExecuteAuthenticatedRequest(t, h.Events, req, nil)
	rr.AssertStatusCode(http.StatusServiceUnavailable)
}

func TestItemEvents_StreamsConnectedEventAndCleansUp(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	itemID := createTestItemForComments(t, tdb, data)

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/events", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	ctx, cancel := context.WithCancel(req.Context())
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)

	w := &syncResponseWriter{writes: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()

	// Connect: the handler sets the SSE content type, emits a `connected` event
	// (the client's full-reconcile trigger), and registers a subscriber.
	w.waitForText(t, "event: connected")
	if subscribers := hub.SubscriberCount(itemID); subscribers != 1 {
		t.Fatalf("subscriber count = %d, want 1", subscribers)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	// A publish (as any mutation chokepoint would emit) reaches the stream.
	hub.PublishItemChange(itemID, services.ItemChangeComment)
	w.waitForText(t, "event: comment")

	// Disconnect: the handler returns and unsubscribes (no leak).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
	if subscribers := hub.SubscriberCount(itemID); subscribers != 0 {
		t.Fatalf("subscriber count after disconnect = %d, want 0", subscribers)
	}
}
