//go:build test

package handlers

import (
	"testing"

	"windshift/internal/models"
)

func TestNotificationManagerDeduplicatesDurableDeliveryBeforePush(t *testing.T) {
	db := newNotificationManagerTestDB(t)
	manager := newNotificationManagerForTest(t, db)
	dispatcher := &recordingPushDispatcher{}
	manager.SetPushDispatcher(dispatcher)
	notification := models.Notification{
		UserID: 1, Title: "Durable", Message: "only once", Type: "info",
		AuthorizationScope: models.NotificationScopeSystem,
		DeliveryKey:        "event-1:rule-2:user-1",
	}
	first, err := manager.AddNotifications([]models.Notification{notification})
	if err != nil {
		t.Fatalf("first durable insert: %v", err)
	}
	second, err := manager.AddNotifications([]models.Notification{notification})
	if err != nil {
		t.Fatalf("replayed durable insert: %v", err)
	}
	if len(first) != 1 || first[0].ID == 0 || len(second) != 0 {
		t.Fatalf("first=%+v second=%+v, want one insert then no-op", first, second)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE delivery_key = ?`, notification.DeliveryKey).Scan(&count); err != nil {
		t.Fatalf("count durable notification: %v", err)
	}
	if count != 1 {
		t.Fatalf("durable rows = %d, want 1", count)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.notifications) != 1 {
		t.Fatalf("push notifications = %d, want 1", len(dispatcher.notifications))
	}
}
