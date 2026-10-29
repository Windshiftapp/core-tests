//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func createRetainedNotification(t *testing.T, handler *NotificationHandler, notification models.Notification) models.Notification {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/notifications", notification)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.CreateNotification, req, nil)
	rr.AssertStatusCode(http.StatusCreated)
	var created models.Notification
	rr.AssertJSONResponse(&created)
	return created
}

func defaultNotificationPage(t *testing.T, handler *NotificationHandler) []models.Notification {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/notifications", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetNotifications, req, nil)
	rr.AssertStatusCode(http.StatusOK)
	var notifications []models.Notification
	rr.AssertJSONResponse(&notifications)
	return notifications
}

func TestNotificationItemReadPersistsBeyondFirstPageAndExcludesEmail(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	t.Cleanup(func() { _ = tdb.Close() })
	// Seed relative to now: the tray only scrolls back notificationTrayRetention,
	// so a fixed 2026 date ages out of the window as the calendar advances.
	base := time.Now().UTC().Add(-24 * time.Hour)
	target := createRetainedNotification(t, handler, models.Notification{
		Title:     "old target",
		Message:   "outside the default page",
		Type:      "comment",
		ActionURL: "/workspaces/1/items/42",
		Timestamp: base,
	})
	for i := 1; i <= 50; i++ {
		createRetainedNotification(t, handler, models.Notification{
			Title:     fmt.Sprintf("newer %d", i),
			Message:   "inside the default page",
			Type:      "info",
			ActionURL: fmt.Sprintf("/workspaces/1/items/%d", 100+i),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	page := defaultNotificationPage(t, handler)
	if len(page) != 50 {
		t.Fatalf("default page length = %d, want 50", len(page))
	}
	for _, notification := range page {
		if notification.ID == target.ID {
			t.Fatalf("old target %d unexpectedly appeared in first page", target.ID)
		}
	}

	markReq := testutils.CreateJSONRequest(t, http.MethodPost, "/api/notifications/mark-item-read", map[string]int{"item_id": 42})
	markRR := testutils.ExecuteAuthenticatedRequest(t, handler.MarkItemNotificationsAsRead, markReq, nil)
	markRR.AssertStatusCode(http.StatusOK)

	var read bool
	if err := tdb.QueryRow("SELECT read FROM notifications WHERE id = ?", target.ID).Scan(&read); err != nil {
		t.Fatalf("query target notification: %v", err)
	}
	if !read {
		t.Fatal("old target remained unread after authoritative item mutation")
	}

	service := services.NewNotificationService(
		tdb.GetDatabase(),
		handler.manager,
		nil,
		services.DefaultNotificationServiceConfig(),
	)
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close notification service: %v", err)
		}
	})
	batches, err := service.UnreadEmailBatches(100)
	if err != nil {
		t.Fatalf("UnreadEmailBatches: %v", err)
	}
	batch := batches["test@example.com"]
	if batch == nil {
		t.Fatal("newer unread notifications were not eligible for email")
	}
	for _, id := range batch.NotificationIDs {
		if id == target.ID {
			t.Fatalf("read target %d remained eligible for notification email", target.ID)
		}
	}
}

func TestNotificationMarkAllPersistsWhenFirstPageIsAlreadyRead(t *testing.T) {
	handler, tdb := setupNotificationHandler(t)
	t.Cleanup(func() { _ = tdb.Close() })
	// Seed relative to now: the tray only scrolls back notificationTrayRetention,
	// so a fixed 2026 date ages out of the window as the calendar advances.
	base := time.Now().UTC().Add(-24 * time.Hour)
	oldest := createRetainedNotification(t, handler, models.Notification{
		Title:     "old unread",
		Message:   "outside the default page",
		Type:      "info",
		Timestamp: base,
	})

	for i := 1; i <= 50; i++ {
		created := createRetainedNotification(t, handler, models.Notification{
			Title:     fmt.Sprintf("newer read %d", i),
			Message:   "inside the default page",
			Type:      "info",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
		markReq := testutils.CreateJSONRequest(t, http.MethodPatch, fmt.Sprintf("/api/notifications/%d/read", created.ID), nil)
		markReq.SetPathValue("id", fmt.Sprintf("%d", created.ID))
		markRR := testutils.ExecuteAuthenticatedRequest(t, handler.MarkNotificationAsRead, markReq, nil)
		markRR.AssertStatusCode(http.StatusOK)
	}

	page := defaultNotificationPage(t, handler)
	if len(page) != 50 {
		t.Fatalf("default page length = %d, want 50", len(page))
	}
	for _, notification := range page {
		if !notification.Read {
			t.Fatalf("loaded notification %d is unread; test requires an all-read first page", notification.ID)
		}
	}

	markAllReq := testutils.CreateJSONRequest(t, http.MethodPatch, "/api/notifications/read-all", nil)
	markAllRR := testutils.ExecuteAuthenticatedRequest(t, handler.MarkAllNotificationsAsRead, markAllReq, nil)
	markAllRR.AssertStatusCode(http.StatusOK)

	var read bool
	if err := tdb.QueryRow("SELECT read FROM notifications WHERE id = ?", oldest.ID).Scan(&read); err != nil {
		t.Fatalf("query oldest notification: %v", err)
	}
	if !read {
		t.Fatal("older notification remained unread after authoritative mark-all mutation")
	}
}
