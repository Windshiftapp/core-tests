//go:build test

package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// mockNotificationService is a no-op notification service for testing.
// Uses concrete type to satisfy the ItemHandler's notification interface.
type mockNotificationService struct{}

// EmitEvent implements the notification service interface with concrete type
func (m *mockNotificationService) EmitEvent(event *services.NotificationEvent) {
	// No-op for tests
}

// grantSystemAdmin grants the system.admin global permission to the given user
// through the permission repository used by the production handler.
// Used to bypass per-endpoint permission checks in handler tests that aren't
// specifically testing permission enforcement.
func grantSystemAdmin(t *testing.T, tdb *testutils.TestDB, userID int) {
	t.Helper()
	setup := newServiceSetup(t, tdb)
	setup.GrantGlobal(userID, "system.admin")
}

// createTestServices creates the services needed for handler tests
func createTestServices(t *testing.T, db testutils.TestDB) (*services.PermissionService, *services.ActivityTracker, *mockNotificationService) {
	t.Helper()

	// Create permission service with test-friendly config
	permConfig := services.DefaultPermissionCacheConfig()
	permConfig.WarmupOnStartup = false // Don't warm up during tests
	permConfig.TTL = 1 * time.Minute   // Short TTL for tests

	permService, err := services.NewPermissionService(db.GetDatabase(), permConfig)
	if err != nil {
		t.Fatalf("Failed to create permission service: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		permService.Close()
	})

	// Create activity tracker with test-friendly config
	actConfig := services.DefaultActivityTrackerConfig()
	actConfig.FlushInterval = 1 * time.Hour // Don't flush during tests
	actConfig.ImmediateFlushActivity = false

	actTracker, err := services.NewActivityTracker(db.GetDatabase(), actConfig)
	if err != nil {
		t.Fatalf("Failed to create activity tracker: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		actTracker.Close()
	})

	// Create mock notification service
	notifService := &mockNotificationService{}

	return permService, actTracker, notifService
}

func TestItemHandler_Create_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Test Item",
		Description: "Test item description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var response models.Item
	rr.AssertJSONResponse(&response)

	if response.ID == 0 {
		t.Error("Expected created item to have an ID")
	}
	if response.Title != item.Title {
		t.Errorf("Expected title %s, got %s", item.Title, response.Title)
	}
	if response.WorkspaceID != item.WorkspaceID {
		t.Errorf("Expected workspace ID %d, got %d", item.WorkspaceID, response.WorkspaceID)
	}
}

func TestItemHandler_Create_WithParent(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create parent item first
	parentItem := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Parent Item",
		Description: "Parent item description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	parentReq := testutils.CreateJSONRequest(t, "POST", "/api/items", parentItem)
	parentRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, parentReq, nil)

	var parentResponse models.Item
	parentRR.AssertJSONResponse(&parentResponse)

	// Create child item
	childItem := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Child Item",
		Description: "Child item description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
		ParentID:    &parentResponse.ID,
	}

	childReq := testutils.CreateJSONRequest(t, "POST", "/api/items", childItem)
	childRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, childReq, nil)

	childRR.AssertStatusCode(http.StatusCreated)

	var childResponse models.Item
	childRR.AssertJSONResponse(&childResponse)

	if childResponse.ParentID == nil || *childResponse.ParentID != parentResponse.ID {
		t.Errorf("Expected parent ID %d, got %v", parentResponse.ID, childResponse.ParentID)
	}
}

func TestItemHandler_Create_ValidationErrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	tests := []struct {
		name           string
		item           models.Item
		expectedErr    string
		expectedStatus int
	}{
		{
			name:           "Missing title",
			item:           models.Item{WorkspaceID: data.WorkspaceID, Title: ""},
			expectedErr:    "Title is required",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid workspace",
			item: models.Item{WorkspaceID: 99999, Title: "Test Item"},
			// Permission check happens before validation, and the security
			// policy returns 404 rather than 403 for workspaces the caller
			// cannot see.
			expectedErr:    "not found",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/items", tt.item)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

			rr.AssertStatusCode(tt.expectedStatus)
			if !strings.Contains(rr.Body.String(), tt.expectedErr) {
				t.Errorf("Expected body to contain %q, got %q", tt.expectedErr, rr.Body.String())
			}
		})
	}
}

func TestItemHandler_GetAll_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create test items
	for i := 0; i < 3; i++ {
		item := models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       "Test Item " + testutils.IntToString(i+1),
			Description: "Test description",
			StatusID:    &statusID,
			PriorityID:  &priorityID,
		}

		req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
		testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/items?workspace_id="+testutils.IntToString(data.WorkspaceID), nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.PaginatedItemsResponse
	rr.AssertJSONResponse(&response)

	if len(response.Items) != 3 {
		t.Errorf("Expected 3 items, got %d", len(response.Items))
	}

	// Check pagination metadata
	if response.Pagination.Total != 3 {
		t.Errorf("Expected total count 3, got %d", response.Pagination.Total)
	}
}

func TestItemHandler_GetAll_CursorWorkspaceList(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID
	for _, title := range []string{"Cursor item 1", "Cursor item 2", "Cursor item 3"} {
		req := testutils.CreateJSONRequest(t, "POST", "/api/items", models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &statusID,
			PriorityID:  &priorityID,
		})
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
		rr.AssertStatusCode(http.StatusCreated)
	}

	baseURL := "/api/items?workspace_id=" + testutils.IntToString(data.WorkspaceID) + "&limit=2&cursor_mode=true"
	firstRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, testutils.CreateJSONRequest(t, "GET", baseURL, nil), nil)
	firstRR.AssertStatusCode(http.StatusOK)
	var first models.PaginatedItemsResponse
	firstRR.AssertJSONResponse(&first)
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first cursor page = items %d next %q, want two items and cursor", len(first.Items), first.NextCursor)
	}

	secondURL := baseURL + "&cursor=" + first.NextCursor
	secondRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, testutils.CreateJSONRequest(t, "GET", secondURL, nil), nil)
	secondRR.AssertStatusCode(http.StatusOK)
	var second models.PaginatedItemsResponse
	secondRR.AssertJSONResponse(&second)
	if len(second.Items) != 1 || second.Items[0].Title != "Cursor item 3" || second.NextCursor != "" {
		t.Fatalf("second cursor page = items %#v next %q, want final item and no cursor", second.Items, second.NextCursor)
	}
}

func TestItemHandler_GetAll_CollectionKeepsOffsetPlanWithCursorParams(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	grantSystemAdmin(t, tdb, 1)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	itemHandler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	collectionHandler := NewCollectionHandler(tdb.GetDatabase(), permService)

	statusID := data.StatusID
	priorityID := data.PriorityID
	createItemRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/items", models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Collection cursor item",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}), nil)
	createItemRR.AssertStatusCode(http.StatusCreated)

	workspaceID := data.WorkspaceID
	createCollectionRR := testutils.ExecuteAuthenticatedRequest(t, collectionHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/collections", models.Collection{
		Name:        "Cursor-compatible collection",
		WorkspaceID: &workspaceID,
		QLQuery:     "status = 'open'",
	}), nil)
	createCollectionRR.AssertStatusCode(http.StatusCreated)
	var collection models.Collection
	createCollectionRR.AssertJSONResponse(&collection)

	listURL := "/api/items?collection_id=" + testutils.IntToString(collection.ID) + "&limit=1&cursor_mode=true&cursor=malformed"
	listRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.GetAll, testutils.CreateJSONRequest(t, "GET", listURL, nil), nil)
	listRR.AssertStatusCode(http.StatusOK)
	var response models.PaginatedItemsResponse
	listRR.AssertJSONResponse(&response)
	if len(response.Items) != 1 || response.Items[0].Title != "Collection cursor item" {
		t.Fatalf("collection list = %#v, want collection item", response.Items)
	}
	if response.NextCursor != "" {
		t.Fatalf("collection next cursor = %q, want empty", response.NextCursor)
	}
}

func TestItemHandler_Get_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create test item
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Get Test Item",
		Description: "Test description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	createReq := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)

	var createdItem models.Item
	createRR.AssertJSONResponse(&createdItem)

	// Get the item
	req := testutils.CreateJSONRequest(t, "GET", "/api/items/"+testutils.IntToString(createdItem.ID), nil)
	req.SetPathValue("id", testutils.IntToString(createdItem.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Item
	rr.AssertJSONResponse(&response)

	if response.ID != createdItem.ID {
		t.Errorf("Expected ID %d, got %d", createdItem.ID, response.ID)
	}
	if response.Title != item.Title {
		t.Errorf("Expected title %s, got %s", item.Title, response.Title)
	}
}

func TestItemHandler_Update_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create test item
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Original Title",
		Description: "Original description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	createReq := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)

	var createdItem models.Item
	createRR.AssertJSONResponse(&createdItem)

	// Update the item
	updateData := map[string]interface{}{
		"title":       "Updated Title",
		"description": "Updated description",
	}

	updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/items/"+testutils.IntToString(createdItem.ID), updateData)
	updateReq.SetPathValue("id", testutils.IntToString(createdItem.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response models.Item
	rr.AssertJSONResponse(&response)

	if response.Title != "Updated Title" {
		t.Errorf("Expected updated title 'Updated Title', got %s", response.Title)
	}
	if response.Description != "Updated description" {
		t.Errorf("Expected updated description 'Updated description', got %s", response.Description)
	}
}

func TestItemHandler_Update_InactiveAssigneeMatchesUnknownUser(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	var inactiveUserID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('inactive-assignee@example.com', 'inactive-assignee', 'Inactive', 'Assignee', 'hash', false)
		RETURNING id
	`).Scan(&inactiveUserID); err != nil {
		t.Fatalf("insert inactive assignee: %v", err)
	}

	createReq := testutils.CreateJSONRequest(t, "POST", "/api/items", models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Assignee visibility item",
	})
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)
	createRR.AssertStatusCode(http.StatusCreated)
	var item models.Item
	createRR.AssertJSONResponse(&item)

	var firstBody string
	for _, assigneeID := range []int{inactiveUserID, 999999} {
		updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/items/"+testutils.IntToString(item.ID), map[string]any{
			"assignee_id": assigneeID,
		})
		updateReq.SetPathValue("id", testutils.IntToString(item.ID))
		updateRR := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)
		updateRR.AssertStatusCode(http.StatusBadRequest)

		body := updateRR.Body.String()
		if firstBody == "" {
			firstBody = body
		} else if body != firstBody {
			t.Fatalf("inactive and unknown assignee responses differ: inactive=%q unknown=%q", firstBody, body)
		}
	}

	var assigneeID *int
	if err := tdb.QueryRow("SELECT assignee_id FROM items WHERE id = ?", item.ID).Scan(&assigneeID); err != nil {
		t.Fatalf("load item assignee: %v", err)
	}
	if assigneeID != nil {
		t.Fatalf("rejected assignee update changed item to user %d", *assigneeID)
	}
}

func TestItemHandler_Delete_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create test item
	item := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Item to Delete",
		Description: "This item will be deleted",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	createReq := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)

	var createdItem models.Item
	createRR.AssertJSONResponse(&createdItem)

	// Delete the item
	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/items/"+testutils.IntToString(createdItem.ID), nil)
	deleteReq.SetPathValue("id", testutils.IntToString(createdItem.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)

	rr.AssertStatusCode(http.StatusNoContent)

	// Verify item is deleted
	getReq := testutils.CreateJSONRequest(t, "GET", "/api/items/"+testutils.IntToString(createdItem.ID), nil)
	getReq.SetPathValue("id", testutils.IntToString(createdItem.ID))
	getRR := testutils.ExecuteAuthenticatedRequest(t, handler.Get, getReq, nil)

	getRR.AssertStatusCode(http.StatusNotFound)
}

func TestItemHandler_GetChildren_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create parent item
	parentItem := models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "Parent Item",
		Description: "Parent item description",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}

	parentReq := testutils.CreateJSONRequest(t, "POST", "/api/items", parentItem)
	parentRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, parentReq, nil)

	var parent models.Item
	parentRR.AssertJSONResponse(&parent)

	// Create child items
	for i := 0; i < 3; i++ {
		childItem := models.Item{
			WorkspaceID: data.WorkspaceID,
			Title:       "Child Item " + testutils.IntToString(i+1),
			Description: "Child description",
			StatusID:    &statusID,
			PriorityID:  &priorityID,
			ParentID:    &parent.ID,
		}

		childReq := testutils.CreateJSONRequest(t, "POST", "/api/items", childItem)
		testutils.ExecuteAuthenticatedRequest(t, handler.Create, childReq, nil)
	}

	// Get children
	req := testutils.CreateJSONRequest(t, "GET", "/api/items/"+testutils.IntToString(parent.ID)+"/children", nil)
	req.SetPathValue("id", testutils.IntToString(parent.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetChildrenNew, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var children []models.Item
	rr.AssertJSONResponse(&children)

	if len(children) != 3 {
		t.Errorf("Expected 3 children, got %d", len(children))
	}

	// Verify all children have correct parent
	for _, child := range children {
		if child.ParentID == nil || *child.ParentID != parent.ID {
			t.Errorf("Expected child to have parent ID %d, got %v", parent.ID, child.ParentID)
		}
	}
}

func TestItemHandler_Search_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	statusID := data.StatusID
	priorityID := data.PriorityID

	// Create test items with different content
	items := []models.Item{
		{
			WorkspaceID: data.WorkspaceID,
			Title:       "Bug in login system",
			Description: "Users cannot log in",
			StatusID:    &statusID,
			PriorityID:  &priorityID,
		},
		{
			WorkspaceID: data.WorkspaceID,
			Title:       "Feature request for dashboard",
			Description: "Add new widgets to dashboard",
			StatusID:    &statusID,
			PriorityID:  &priorityID,
		},
		{
			WorkspaceID: data.WorkspaceID,
			Title:       "Documentation update",
			Description: "Update API documentation",
			StatusID:    &statusID,
			PriorityID:  &priorityID,
		},
	}

	for _, item := range items {
		req := testutils.CreateJSONRequest(t, "POST", "/api/items", item)
		testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	}

	// Search for items containing "dashboard"
	searchReq := testutils.CreateJSONRequest(t, "GET", "/api/items/search?q=dashboard", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Search, searchReq, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var results []models.Item
	rr.AssertJSONResponse(&results)

	if len(results) != 1 {
		t.Errorf("Expected 1 search result, got %d", len(results))
	}

	if len(results) > 0 {
		if results[0].Title != "Feature request for dashboard" {
			t.Errorf("Expected to find dashboard item, got %s", results[0].Title)
		}
	}
}
