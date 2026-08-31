//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
	"windshift/internal/testutils/mocks"
)

// errorNotificationService is a mock that returns errors from ForceRefreshCache
type errorNotificationService struct{}

func (e *errorNotificationService) ForceRefreshCache() error {
	return fmt.Errorf("cache refresh failed")
}

func setupNotificationHandler(t *testing.T) (*NotificationHandler, *testutils.TestDB) {
	tdb := testutils.CreateTestDB(t, true)
	tdb.SeedTestData(t)

	manager, err := NewNotificationManager(tdb.GetDatabase(), DefaultNotificationManagerConfig())
	if err != nil {
		tdb.Close()
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	t.Cleanup(func() { manager.Stop() })

	service := mocks.CreateMockNotificationService()
	handler := NewNotificationHandler(manager, service)
	return handler, tdb
}

// --- GetNotifications ---

func TestNotificationHandler_GetNotifications_Unauthenticated(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "GET", "/api/notifications", nil)
	rr := testutils.ExecuteRequest(t, handler.GetNotifications, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestNotificationHandler_GetNotifications_Empty(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "GET", "/api/notifications", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var notifications []models.Notification
	rr.AssertJSONResponse(&notifications)

	if len(notifications) != 0 {
		t.Errorf("Expected 0 notifications, got %d", len(notifications))
	}
}

func TestNotificationHandler_GetNotifications_WithData(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	// Create notifications via POST
	for i := 0; i < 3; i++ {
		notification := models.Notification{
			UserID:  1,
			Title:   fmt.Sprintf("Test Notification %d", i+1),
			Message: fmt.Sprintf("Message %d", i+1),
			Type:    "info",
		}
		createReq := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
		testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, createReq, nil)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/notifications", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var notifications []models.Notification
	rr.AssertJSONResponse(&notifications)

	if len(notifications) != 3 {
		t.Errorf("Expected 3 notifications, got %d", len(notifications))
	}
}

func TestNotificationHandler_GetNotifications_CustomPagination(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	// Create 5 notifications
	for i := 0; i < 5; i++ {
		notification := models.Notification{
			UserID:  1,
			Title:   fmt.Sprintf("Notification %d", i+1),
			Message: fmt.Sprintf("Message %d", i+1),
			Type:    "info",
		}
		createReq := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
		testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, createReq, nil)
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/notifications?limit=2&offset=1", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var notifications []models.Notification
	rr.AssertJSONResponse(&notifications)

	if len(notifications) != 2 {
		t.Errorf("Expected 2 notifications with limit=2&offset=1, got %d", len(notifications))
	}
}

func TestNotificationHandler_GetNotifications_PaginationBounds(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	// Create 2 notifications
	for i := 0; i < 2; i++ {
		notification := models.Notification{
			UserID:  1,
			Title:   fmt.Sprintf("Notification %d", i+1),
			Message: fmt.Sprintf("Message %d", i+1),
			Type:    "info",
		}
		createReq := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
		testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, createReq, nil)
	}

	// High offset returns empty
	req := testutils.CreateJSONRequest(t, "GET", "/api/notifications?offset=100", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var notifications []models.Notification
	rr.AssertJSONResponse(&notifications)
	if len(notifications) != 0 {
		t.Errorf("Expected 0 notifications with high offset, got %d", len(notifications))
	}

	// Invalid limit/offset uses defaults
	req2 := testutils.CreateJSONRequest(t, "GET", "/api/notifications?limit=abc&offset=xyz", nil)
	rr2 := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req2, nil)
	rr2.AssertStatusCode(http.StatusOK)

	var notifications2 []models.Notification
	rr2.AssertJSONResponse(&notifications2)
	if len(notifications2) != 2 {
		t.Errorf("Expected 2 notifications with invalid params (defaults), got %d", len(notifications2))
	}
}

func TestNotificationHandler_GetNotifications_FiltersRevokedWorkspaceProvenance(t *testing.T) {
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
		Name:      "Notification revocation",
		Key:       "NRV",
		CreatorID: keeperID,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var viewerRoleID int
	if err := db.QueryRow("SELECT id FROM workspace_roles WHERE name = 'Viewer'").Scan(&viewerRoleID); err != nil {
		t.Fatalf("load viewer role: %v", err)
	}
	roles := repository.NewWorkspaceRoleRepository(db)
	if err := roles.AssignToUser(keeperID, workspaceID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("lock down workspace: %v", err)
	}
	if err := roles.AssignToUser(1, workspaceID, viewerRoleID, keeperID); err != nil {
		t.Fatalf("assign viewer role: %v", err)
	}
	permissions, err := services.NewPermissionService(db, services.DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	manager, err := NewNotificationManager(db, DefaultNotificationManagerConfig())
	if err != nil {
		t.Fatalf("create notification manager: %v", err)
	}
	t.Cleanup(manager.Stop)
	itemID := 42
	workspaceNotification := models.Notification{
		UserID:             1,
		Title:              "Restricted workspace title",
		Message:            "Restricted workspace message",
		Type:               "comment",
		ActionURL:          fmt.Sprintf("/workspaces/%d/items/42", workspaceID),
		Metadata:           `{"restricted":true}`,
		AuthorizationScope: models.NotificationScopeWorkspace,
		WorkspaceID:        &workspaceID,
		ItemID:             &itemID,
		SourceType:         "comment.created",
		SourceID:           &itemID,
	}
	if _, err := manager.AddNotifications([]models.Notification{
		workspaceNotification,
		{UserID: 1, Title: "System notice", Message: "Still visible", Type: "info", AuthorizationScope: models.NotificationScopeSystem},
	}); err != nil {
		t.Fatalf("seed notifications: %v", err)
	}
	handler := NewNotificationHandler(manager, nil, permissions)

	get := func() []models.Notification {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/notifications", nil)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var notifications []models.Notification
		rr.AssertJSONResponse(&notifications)
		return notifications
	}
	if before := get(); len(before) != 2 {
		t.Fatalf("notifications before revocation = %+v, want workspace and system rows", before)
	}
	if _, err := roles.RevokeFromUser(1, workspaceID, viewerRoleID); err != nil {
		t.Fatalf("revoke viewer role: %v", err)
	}
	if err := permissions.InvalidateUserCache(1); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}
	after := get()
	if len(after) != 1 || after[0].Title != "System notice" {
		t.Fatalf("notifications after revocation = %+v, want system row only", after)
	}
}

// --- ClearNotifications ---

func TestNotificationHandler_ClearNotifications_Unauthenticated(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/notifications", nil)
	rr := testutils.ExecuteRequest(t, handler.ClearNotifications, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestNotificationHandler_ClearNotifications_DeletesOnlyCurrentUsersInbox(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()
	otherUserID, err := factory.NewTestFactory(tdb.GetDatabase()).CreateUser(nil)
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	for _, notification := range []models.Notification{
		{UserID: 1, Title: "First", Message: "Current user", Type: "info", AuthorizationScope: models.NotificationScopeSystem},
		{UserID: 1, Title: "Second", Message: "Current user", Type: "info", AuthorizationScope: models.NotificationScopeSystem},
		{UserID: otherUserID, Title: "Other", Message: "Other user", Type: "info", AuthorizationScope: models.NotificationScopeSystem},
	} {
		if _, err := handler.manager.AddNotification(notification); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
	}
	if got, err := handler.manager.GetUserNotifications(1, 50, 0); err != nil || len(got) != 2 {
		t.Fatalf("warm current user cache: len=%d err=%v", len(got), err)
	}

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/notifications", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ClearNotifications, req, nil)
	rr.AssertStatusCode(http.StatusNoContent)

	currentUserNotifications, err := handler.manager.GetUserNotifications(1, 50, 0)
	if err != nil {
		t.Fatalf("get current user notifications: %v", err)
	}
	if len(currentUserNotifications) != 0 {
		t.Fatalf("current user notifications = %d, want 0", len(currentUserNotifications))
	}

	otherUserNotifications, err := handler.manager.GetUserNotifications(otherUserID, 50, 0)
	if err != nil {
		t.Fatalf("get other user notifications: %v", err)
	}
	if len(otherUserNotifications) != 1 || otherUserNotifications[0].Title != "Other" {
		t.Fatalf("other user notifications = %+v, want the existing notification", otherUserNotifications)
	}
}

// --- CreateNotification ---

func TestNotificationHandler_CreateNotification_Success(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	notification := models.Notification{
		UserID:  1,
		Title:   "New Assignment",
		Message: "You have been assigned to task #42",
		Type:    "assignment",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var response models.Notification
	rr.AssertJSONResponse(&response)

	if response.Title != notification.Title {
		t.Errorf("Expected title %q, got %q", notification.Title, response.Title)
	}
	if response.Message != notification.Message {
		t.Errorf("Expected message %q, got %q", notification.Message, response.Message)
	}
	if response.Timestamp.IsZero() {
		t.Error("Expected timestamp to be auto-set")
	}
}

func TestNotificationHandler_CreateNotification_WithTimestamp(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	customTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	notification := models.Notification{
		UserID:    1,
		Title:     "Scheduled Notification",
		Message:   "This has a custom timestamp",
		Type:      "info",
		Timestamp: customTime,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var response models.Notification
	rr.AssertJSONResponse(&response)

	if !response.Timestamp.Equal(customTime) {
		t.Errorf("Expected timestamp %v, got %v", customTime, response.Timestamp)
	}
}

func TestNotificationHandler_CreateNotification_InvalidJSON(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications", nil)
	req.Body = http.NoBody
	// Write invalid JSON
	req, _ = http.NewRequest("POST", "/api/notifications", strings.NewReader("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req = testutils.WithAuthContext(req, nil)

	rr := testutils.ExecuteRequest(t, handler.CreateNotification, req)

	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestNotificationHandler_CreateNotification_AllFields(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	notification := models.Notification{
		UserID:    1,
		Title:     "Full Notification",
		Message:   "All fields populated",
		Type:      "comment",
		Avatar:    "JD",
		ActionURL: "/items/42",
		Metadata:  `{"item_id":42,"workspace":"TEST"}`,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var response models.Notification
	rr.AssertJSONResponse(&response)

	if response.Avatar != notification.Avatar {
		t.Errorf("Expected avatar %q, got %q", notification.Avatar, response.Avatar)
	}
	if response.ActionURL != notification.ActionURL {
		t.Errorf("Expected action_url %q, got %q", notification.ActionURL, response.ActionURL)
	}
	if response.Metadata != notification.Metadata {
		t.Errorf("Expected metadata %q, got %q", notification.Metadata, response.Metadata)
	}
}

// --- MarkNotificationAsRead ---

func TestNotificationHandler_MarkNotificationAsRead_Success(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	// Create a notification
	notification := models.Notification{
		UserID:  1,
		Title:   "Unread Notification",
		Message: "This should be marked as read",
		Type:    "info",
	}
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/notifications", notification)
	testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, createReq, nil)

	// Get the notification ID from cache (CreateNotification returns the pre-AddNotification copy with ID=0,
	// so we need to fetch from GET to get the real cache ID)
	getReq := testutils.CreateJSONRequest(t, "GET", "/api/notifications", nil)
	getRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, getReq, nil)

	var notifications []models.Notification
	getRR.AssertJSONResponse(&notifications)

	if len(notifications) == 0 {
		t.Fatal("Expected at least 1 notification")
	}
	notifID := notifications[0].ID

	// Mark as read
	markReq := testutils.CreateJSONRequest(t, "PATCH", fmt.Sprintf("/api/notifications/%d/read", notifID), nil)
	markReq.SetPathValue("id", testutils.IntToString(notifID))
	markRR := testutils.ExecuteAuthenticatedRequest(t, handler.MarkNotificationAsRead, markReq, nil)

	markRR.AssertStatusCode(http.StatusOK)

	// Verify via GET that it's read
	getReq2 := testutils.CreateJSONRequest(t, "GET", "/api/notifications", nil)
	getRR2 := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, getReq2, nil)

	var updated []models.Notification
	getRR2.AssertJSONResponse(&updated)

	found := false
	for _, n := range updated {
		if n.ID == notifID {
			found = true
			if !n.Read {
				t.Error("Expected notification to be marked as read")
			}
		}
	}
	if !found {
		t.Error("Notification not found in GET response after marking as read")
	}
}

func TestNotificationHandler_MarkNotificationAsRead_Unauthenticated(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "PATCH", "/api/notifications/1/read", nil)
	req.SetPathValue("id", "1")
	rr := testutils.ExecuteRequest(t, handler.MarkNotificationAsRead, req)

	rr.AssertStatusCode(http.StatusUnauthorized)
}

func TestNotificationHandler_MarkNotificationAsRead_InvalidID(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "PATCH", "/api/notifications/abc/read", nil)
	req.SetPathValue("id", "abc")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.MarkNotificationAsRead, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- RefreshCache ---

func TestNotificationHandler_RefreshCache_Success(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	defer tdb.Close()

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications/refresh-cache", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RefreshCache, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var response map[string]string
	rr.AssertJSONResponse(&response)

	if !strings.Contains(response["message"], "refreshed successfully") {
		t.Errorf("Expected success message, got %q", response["message"])
	}
}

func TestNotificationHandler_RefreshCache_NoService(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)

	manager, err := NewNotificationManager(tdb.GetDatabase(), DefaultNotificationManagerConfig())
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Stop()

	// Create handler with nil service
	handler := NewNotificationHandler(manager, nil)

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications/refresh-cache", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RefreshCache, req, nil)

	rr.AssertStatusCode(http.StatusInternalServerError)
}

func TestNotificationHandler_RefreshCache_ServiceError(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)

	manager, err := NewNotificationManager(tdb.GetDatabase(), DefaultNotificationManagerConfig())
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Stop()

	// Create handler with error-returning service
	handler := NewNotificationHandler(manager, &errorNotificationService{})

	req := testutils.CreateJSONRequest(t, "POST", "/api/notifications/refresh-cache", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.RefreshCache, req, nil)

	rr.AssertStatusCode(http.StatusInternalServerError)
}
