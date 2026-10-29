//go:build test

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func lockDownWorkspaceForViewer(t *testing.T, tdb *testutils.TestDB, workspaceID, keeperID int, grantUserOne bool) {
	t.Helper()
	db := tdb.GetDatabase()
	var viewerRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, models.RoleViewer).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load viewer role: %v", err)
	}
	roles := repository.NewWorkspaceRoleRepository(db)
	if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("lock down workspace: %v", err)
	}
	if grantUserOne {
		if err := roles.AssignToUser(1, workspaceID, viewerRoleID, keeperID); err != nil {
			t.Fatalf("assign viewer role: %v", err)
		}
	}
}

func TestWorkspaceEvents_503WhenHubDisabled(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)

	h := newItemHandlerWithHub(t, tdb, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/1/events", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteAuthenticatedRequest(t, h.WorkspaceEvents, req, nil)
	rr.AssertStatusCode(http.StatusServiceUnavailable)
}

func TestWorkspaceEvents_StreamsConnectedAndPublishes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "SSE ws", Key: "SSEW", CreatorID: keeperID})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	lockDownWorkspaceForViewer(t, tdb, workspaceID, keeperID, true)

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/workspaces/%d/events", workspaceID), nil)
	req.SetPathValue("id", testutils.IntToString(workspaceID))
	ctx, cancel := context.WithCancel(req.Context())
	t.Cleanup(cancel)
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)
	w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() {
		h.WorkspaceEvents(w, req)
		close(done)
	}()

	waitForCond(t, "connected frame", func() bool {
		return strings.Contains(w.String(), "event: connected")
	})
	waitForCond(t, "subscriber registered", func() bool {
		return hub.WorkspaceSubscriberCount(workspaceID) == 1
	})
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	hub.PublishWorkspaceChange(workspaceID)
	waitForCond(t, "changed frame", func() bool {
		return strings.Contains(w.String(), "event: changed")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
	waitForCond(t, "subscriber cleaned up", func() bool {
		return hub.WorkspaceSubscriberCount(workspaceID) == 0
	})
}

func TestWorkspaceEvents_DeniesWithoutItemView(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "SSE hidden", Key: "SSEH", CreatorID: keeperID})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	lockDownWorkspaceForViewer(t, tdb, workspaceID, keeperID, false)

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/workspaces/%d/events", workspaceID), nil)
	req.SetPathValue("id", testutils.IntToString(workspaceID))
	rr := testutils.ExecuteAuthenticatedRequest(t, h.WorkspaceEvents, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
	if got := hub.WorkspaceSubscriberCount(workspaceID); got != 0 {
		t.Fatalf("denied subscription registered %d subscribers, want 0", got)
	}
}

func TestCollectionEvents_WorkspaceScopeStreams(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "SSE coll", Key: "SSEC", CreatorID: keeperID})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	lockDownWorkspaceForViewer(t, tdb, workspaceID, keeperID, true)

	collection := &models.Collection{Name: "Scoped collection", WorkspaceID: &workspaceID}
	if err := repository.NewCollectionRepository(db).Create(collection, keeperID); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/collections/%d/events", collection.ID), nil)
	req.SetPathValue("key", testutils.IntToString(collection.ID))
	ctx, cancel := context.WithCancel(req.Context())
	t.Cleanup(cancel)
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)
	w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() {
		h.CollectionEvents(w, req)
		close(done)
	}()

	waitForCond(t, "connected frame", func() bool {
		return strings.Contains(w.String(), "event: connected")
	})
	waitForCond(t, "subscriber registered", func() bool {
		return hub.WorkspaceSubscriberCount(workspaceID) == 1
	})

	hub.PublishWorkspaceChange(workspaceID)
	waitForCond(t, "changed frame", func() bool {
		return strings.Contains(w.String(), "event: changed")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
}

func TestCollectionEvents_DeniesInaccessibleWorkspaceCollection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "SSE coll hidden", Key: "SSCH", CreatorID: keeperID})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	lockDownWorkspaceForViewer(t, tdb, workspaceID, keeperID, false)

	collection := &models.Collection{Name: "Hidden collection", WorkspaceID: &workspaceID}
	if err := repository.NewCollectionRepository(db).Create(collection, keeperID); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/collections/%d/events", collection.ID), nil)
	req.SetPathValue("key", testutils.IntToString(collection.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, h.CollectionEvents, req, nil)
	rr.AssertStatusCode(http.StatusNotFound)
	if got := hub.WorkspaceSubscriberCount(workspaceID); got != 0 {
		t.Fatalf("denied collection subscription registered %d subscribers, want 0", got)
	}
}

func TestCollectionEvents_GlobalScopeSubscribesAccessibleWorkspaces(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	keeperID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create keeper: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{Name: "SSE global", Key: "SSEG", CreatorID: keeperID})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	lockDownWorkspaceForViewer(t, tdb, workspaceID, keeperID, true)

	collection := &models.Collection{Name: "Global collection"}
	if err := repository.NewCollectionRepository(db).Create(collection, keeperID); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	hub := services.NewSSEHub()
	h := newItemHandlerWithHub(t, tdb, hub)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/collections/%d/events", collection.ID), nil)
	req.SetPathValue("key", testutils.IntToString(collection.ID))
	ctx, cancel := context.WithCancel(req.Context())
	t.Cleanup(cancel)
	req = testutils.WithAuthContext(req.WithContext(ctx), nil)
	w := &syncResponseWriter{flushed: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() {
		h.CollectionEvents(w, req)
		close(done)
	}()

	waitForCond(t, "connected frame", func() bool {
		return strings.Contains(w.String(), "event: connected")
	})
	waitForCond(t, "accessible workspace subscribed", func() bool {
		return hub.WorkspaceSubscriberCount(workspaceID) == 1
	})

	hub.PublishWorkspaceChange(workspaceID)
	waitForCond(t, "changed frame", func() bool {
		return strings.Contains(w.String(), "event: changed")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
}
