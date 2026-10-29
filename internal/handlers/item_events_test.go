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

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// syncResponseWriter is a thread-safe ResponseWriter+Flusher so the test can
// read the streamed output while the blocking SSE handler writes from another
// goroutine.
type syncResponseWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	hdr     http.Header
	flushed chan struct{}
}

func (w *syncResponseWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *syncResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncResponseWriter) WriteHeader(int) {}
func (w *syncResponseWriter) Flush() {
	if w.flushed == nil {
		return
	}
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func (w *syncResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		<-time.After(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
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

	w := &syncResponseWriter{}
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()

	// Connect: the handler sets the SSE content type, emits a `connected` event
	// (the client's full-reconcile trigger), and registers a subscriber.
	waitForCond(t, "connected frame", func() bool {
		return strings.Contains(w.String(), "event: connected")
	})
	waitForCond(t, "subscriber registered", func() bool {
		return hub.SubscriberCount(itemID) == 1
	})
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	// A publish (as any mutation chokepoint would emit) reaches the stream as a
	// coarse change frame.
	hub.PublishItemChange(itemID)
	waitForCond(t, "change event frame", func() bool {
		return strings.Contains(w.String(), "event: changed")
	})

	// Disconnect: the handler returns and unsubscribes (no leak).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
	waitForCond(t, "subscriber cleaned up", func() bool {
		return hub.SubscriberCount(itemID) == 0
	})
}

func TestItemEvents_ReauthorizesBeforePublishingAfterWorkspaceRevocation(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%t", deleted), func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			tdb.SeedTestData(t)
			t.Cleanup(func() { _ = tdb.Close() })
			db := tdb.GetDatabase()
			f := factory.NewTestFactory(db)
			keeperID, err := f.CreateUser(nil)
			if err != nil {
				t.Fatalf("create workspace keeper: %v", err)
			}
			workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{
				Name: "SSE revocation", Key: "SSER", CreatorID: keeperID,
			})
			if err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			itemID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: workspaceID, CreatorID: &keeperID})
			if err != nil {
				t.Fatalf("create item: %v", err)
			}
			var viewerRoleID int
			if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, models.RoleViewer).Scan(&viewerRoleID); err != nil {
				t.Fatalf("load viewer role: %v", err)
			}
			roles := repository.NewWorkspaceRoleRepository(db)
			if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
				t.Fatalf("lock down workspace: %v", err)
			}
			if err := roles.AssignToUser(1, workspaceID, viewerRoleID, keeperID); err != nil {
				t.Fatalf("assign viewer role: %v", err)
			}

			hub := services.NewSSEHub()
			h := newItemHandlerWithHub(t, tdb, hub)
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/events", itemID), nil)
			req.SetPathValue("id", testutils.IntToString(itemID))
			ctx, cancel := context.WithCancel(req.Context())
			t.Cleanup(cancel)
			req = testutils.WithAuthContext(req.WithContext(ctx), nil)
			w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
			done := make(chan struct{})
			go func() {
				h.Events(w, req)
				close(done)
			}()

			select {
			case <-w.flushed:
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not flush its connected frame")
			}
			if !strings.Contains(w.String(), "event: connected") {
				t.Fatalf("initial stream = %q, want connected event", w.String())
			}
			if _, err := roles.RevokeFromUser(1, workspaceID, viewerRoleID); err != nil {
				t.Fatalf("revoke viewer role: %v", err)
			}
			if err := h.permissionService.InvalidateUserCache(1); err != nil {
				t.Fatalf("invalidate permission cache: %v", err)
			}
			if deleted {
				if err := services.NewItemCRUDService(db).DeleteSingle(itemID); err != nil {
					t.Fatalf("delete item: %v", err)
				}
				hub.PublishItemDeletion(itemID, workspaceID)
			} else {
				hub.PublishItemChange(itemID)
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not close before publishing a post-revocation event")
			}
			if strings.Count(w.String(), "event:") != 1 {
				t.Fatalf("stream leaked post-revocation event: %q", w.String())
			}
		})
	}
}

func TestItemEvents_DeletionFlushesAndClosesStream(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	tdb.SeedTestData(t)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create workspace keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{
		Name: "SSE revocation", Key: "SSER", CreatorID: keeperID,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	itemID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: workspaceID, CreatorID: &keeperID})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	var viewerRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, models.RoleViewer).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load viewer role: %v", err)
	}
	roles := repository.NewWorkspaceRoleRepository(db)
	if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("lock down workspace: %v", err)
	}
	if err := roles.AssignToUser(1, workspaceID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("assign viewer role: %v", err)
	}

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/events", itemID), nil)
	req.SetPathValue("id", testutils.IntToString(itemID))
	ctx, cancel := context.WithCancel(req.Context())
	t.Cleanup(cancel)
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)
	w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() {
		h.Events(w, req)
		close(done)
	}()

	select {
	case <-w.flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not flush its connected frame")
	}
	if !strings.Contains(w.String(), "event: connected") {
		t.Fatalf("initial stream = %q, want connected event", w.String())
	}
	// Delete through the production service before publishing the committed event.
	if err := services.NewItemCRUDService(db).DeleteSingle(itemID); err != nil {
		t.Fatalf("delete item: %v", err)
	}

	hub.PublishItemDeletion(itemID, workspaceID)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after deletion")
	}
	want := fmt.Sprintf("event: deleted\ndata: {\"item_id\":%d,\"kind\":\"deleted\"}\n\n", itemID)
	if !strings.HasSuffix(w.String(), want) {
		t.Fatalf("stream = %q, want terminal frame %q", w.String(), want)
	}
	if hub.SubscriberCount(itemID) != 0 {
		t.Fatal("deleted item stream remained subscribed")
	}
}

func TestItemEvents_ReauthorizesAgainstWorkspaceAfterItemMove(t *testing.T) {
	for _, test := range []struct {
		name                   string
		grantDestinationAccess bool
		deleteAfterMove        bool
		wantEvent              bool
	}{
		{name: "inaccessible destination closes before disclosure"},
		{name: "accessible destination continues stream", grantDestinationAccess: true, wantEvent: true},
		{name: "inaccessible destination deletion closes without disclosure", deleteAfterMove: true},
		{name: "accessible destination deletion flushes terminal event", grantDestinationAccess: true, deleteAfterMove: true, wantEvent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			tdb.SeedTestData(t)
			t.Cleanup(func() { _ = tdb.Close() })
			db := tdb.GetDatabase()
			f := factory.NewTestFactory(db)
			keeperID, err := f.CreateUser(nil)
			if err != nil {
				t.Fatalf("create workspace keeper: %v", err)
			}
			sourceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{
				Name: "SSE move source", Key: "SSEM", CreatorID: keeperID,
			})
			if err != nil {
				t.Fatalf("create source workspace: %v", err)
			}
			destinationID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{
				Name: "SSE move destination", Key: "SSED", CreatorID: keeperID,
			})
			if err != nil {
				t.Fatalf("create destination workspace: %v", err)
			}
			itemID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: sourceID, CreatorID: &keeperID})
			if err != nil {
				t.Fatalf("create item: %v", err)
			}
			var viewerRoleID int
			if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, models.RoleViewer).Scan(&viewerRoleID); err != nil {
				t.Fatalf("load viewer role: %v", err)
			}
			roles := repository.NewWorkspaceRoleRepository(db)
			for _, workspaceID := range []int{sourceID, destinationID} {
				if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
					t.Fatalf("lock down workspace %d: %v", workspaceID, err)
				}
			}
			if err := roles.AssignToUser(1, sourceID, viewerRoleID, keeperID); err != nil {
				t.Fatalf("assign source viewer: %v", err)
			}
			if test.grantDestinationAccess {
				if err := roles.AssignToUser(1, destinationID, viewerRoleID, keeperID); err != nil {
					t.Fatalf("assign destination viewer: %v", err)
				}
			}

			hub := services.NewSSEHub()
			h := newItemHandlerWithHub(t, tdb, hub)
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/items/%d/events", itemID), nil)
			req.SetPathValue("id", testutils.IntToString(itemID))
			ctx, cancel := context.WithCancel(req.Context())
			t.Cleanup(cancel)
			req = testutils.WithAuthContext(req.WithContext(ctx), nil)
			w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
			done := make(chan struct{})
			go func() {
				h.Events(w, req)
				close(done)
			}()

			select {
			case <-w.flushed:
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not flush its connected frame")
			}
			// Apply the committed workspace-id effect of ItemWorkspaceMoveService.
			// The stream regression does not need the move service's taxonomy remapping.
			if _, err := db.ExecWrite(`UPDATE items SET workspace_id = ? WHERE id = ?`, destinationID, itemID); err != nil {
				t.Fatalf("move item: %v", err)
			}
			if err := h.permissionService.InvalidateUserCache(1); err != nil {
				t.Fatalf("invalidate permission cache: %v", err)
			}
			// Every non-deletion publish is delivered as the coarse `changed`
			// event; a deletion is delivered as `deleted`.
			wantEvent := "changed"
			if test.deleteAfterMove {
				if err := services.NewItemCRUDService(db).DeleteSingle(itemID); err != nil {
					t.Fatalf("delete moved item: %v", err)
				}
				hub.PublishItemDeletion(itemID, destinationID)
				wantEvent = "deleted"
			} else {
				hub.PublishItemChange(itemID)
			}

			if test.wantEvent {
				select {
				case <-w.flushed:
				case <-time.After(2 * time.Second):
					t.Fatal("stream did not flush the permitted post-move event")
				}
				if !strings.Contains(w.String(), "event: "+wantEvent) {
					t.Fatalf("stream omitted permitted post-move event: %q", w.String())
				}
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("stream did not close after cancellation")
				}
				return
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not close before disclosing the inaccessible post-move event")
			}
			if strings.Contains(w.String(), "event: "+wantEvent) {
				t.Fatalf("stream leaked inaccessible post-move event: %q", w.String())
			}
		})
	}
}
