package handlers

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

type recordingPushDispatcher struct {
	mu            sync.Mutex
	notifications []models.Notification
	reject        bool
}

func (d *recordingPushDispatcher) Enqueue(notification models.Notification) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reject {
		return false
	}
	d.notifications = append(d.notifications, notification)
	return true
}

func (d *recordingPushDispatcher) Close(context.Context) error { return nil }

func newNotificationManagerTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatalf("new SQLite database: %v", err)
	}
	statements := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'info',
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			read BOOLEAN DEFAULT false,
			seen_at DATETIME,
			sent_at DATETIME,
			last_send_failed BOOLEAN DEFAULT FALSE,
			avatar TEXT,
			action_url TEXT,
			metadata TEXT,
			authorization_scope TEXT NOT NULL DEFAULT 'legacy',
			workspace_id INTEGER,
			item_id INTEGER,
			source_type TEXT,
			source_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`INSERT INTO users (id) VALUES (1), (2), (3)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecWrite(statement); err != nil {
			_ = db.Close()
			t.Fatalf("initialize notification database: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newNotificationManagerForTest(t *testing.T, db database.Database) *NotificationManager {
	t.Helper()
	config := DefaultNotificationManagerConfig()
	config.MaxBatchSize = 25
	manager, err := NewNotificationManager(db, config)
	if err != nil {
		t.Fatalf("new notification manager: %v", err)
	}
	t.Cleanup(manager.Stop)
	return manager
}

func testNotifications(userID, count int) []models.Notification {
	notifications := make([]models.Notification, count)
	for i := range notifications {
		notifications[i] = models.Notification{
			UserID:             userID,
			Title:              fmt.Sprintf("Notification %d", i),
			Message:            "bounded payload",
			Type:               "info",
			ActionURL:          fmt.Sprintf("/workspaces/1/items/%d", i+1),
			AuthorizationScope: models.NotificationScopeSystem,
		}
	}
	return notifications
}

func TestNotificationManagerBulkInsertAndCompactCache(t *testing.T) {
	db := newNotificationManagerTestDB(t)
	manager := newNotificationManagerForTest(t, db)
	dispatcher := &recordingPushDispatcher{}
	manager.SetPushDispatcher(dispatcher)

	// Warm an empty complete snapshot so inserts update rather than invalidate it.
	if got, err := manager.GetUserNotifications(1, 50, 0); err != nil || len(got) != 0 {
		t.Fatalf("warm cache: len=%d err=%v", len(got), err)
	}
	stored, err := manager.AddNotifications(testNotifications(1, 1000))
	if err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	if len(stored) != 1000 || stored[0].ID == 0 || stored[999].ID == 0 {
		t.Fatalf("stored ids not populated: first=%d last=%d count=%d", stored[0].ID, stored[999].ID, len(stored))
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = 1`).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if count != 1000 {
		t.Fatalf("database count = %d, want 1000", count)
	}
	cache, ok := manager.cacheSnapshot(1)
	if !ok || len(cache.Notifications) != notificationCachePageSize || cache.Complete {
		t.Fatalf("cache snapshot = ok:%v len:%d complete:%v", ok, len(cache.Notifications), cache.Complete)
	}
	if page, err := manager.GetUserNotifications(1, 10, 90); err != nil || len(page) != 10 {
		t.Fatalf("cached page: len=%d err=%v", len(page), err)
	}
	if page, err := manager.GetUserNotifications(1, 20, 100); err != nil || len(page) != 20 {
		t.Fatalf("deep page: len=%d err=%v", len(page), err)
	}
	stats := manager.GetStats()
	if stats["insert_batches"] != 1 || stats["inserted"] != 1000 || stats["max_cache_entry_bytes"] <= 0 {
		t.Fatalf("manager stats = %+v", stats)
	}
	dispatcher.mu.Lock()
	pushCount := len(dispatcher.notifications)
	dispatcher.mu.Unlock()
	if pushCount != 1000 {
		t.Fatalf("push enqueue count = %d, want 1000", pushCount)
	}
}

func TestNotificationManagerSlowUserDoesNotBlockAnotherUser(t *testing.T) {
	db := newNotificationManagerTestDB(t)
	manager := newNotificationManagerForTest(t, db)

	blockedLock := manager.userLock(1)
	blockedLock.Lock()
	blockedDone := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := manager.AddNotification(testNotifications(1, 1)[0])
		blockedDone <- err
	}()
	<-started

	otherDone := make(chan error, 1)
	go func() {
		_, err := manager.AddNotification(testNotifications(2, 1)[0])
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("other user insert: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated user was blocked by another user lock")
	}
	blockedLock.Unlock()
	if err := <-blockedDone; err != nil {
		t.Fatalf("blocked user insert: %v", err)
	}
}

func TestNotificationManagerConcurrentCreateReadUpdate(t *testing.T) {
	db := newNotificationManagerTestDB(t)
	manager := newNotificationManagerForTest(t, db)
	initial, err := manager.AddNotifications(testNotifications(1, 50))
	if err != nil {
		t.Fatalf("seed notifications: %v", err)
	}

	var workers sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 50; i++ {
				switch worker % 3 {
				case 0:
					_, _ = manager.AddNotification(testNotifications((worker%2)+1, 1)[0])
				case 1:
					_, _ = manager.GetUserNotifications(1, 50, 0)
				case 2:
					_ = manager.MarkAsRead(1, initial[i%len(initial)].ID)
				}
			}
		}(worker)
	}
	workers.Wait()
	if page, err := manager.GetUserNotifications(1, 100, 0); err != nil || len(page) == 0 || len(page) > notificationCachePageSize {
		t.Fatalf("final page: len=%d err=%v", len(page), err)
	}
}

func TestNotificationManagerMarkAllAsReadUpdatesDatabaseAndWarmCache(t *testing.T) {
	db := newNotificationManagerTestDB(t)
	manager := newNotificationManagerForTest(t, db)

	if _, err := manager.AddNotifications(testNotifications(1, 3)); err != nil {
		t.Fatalf("seed notifications: %v", err)
	}
	if _, err := manager.AddNotifications(testNotifications(2, 1)); err != nil {
		t.Fatalf("seed other user notification: %v", err)
	}
	if page, err := manager.GetUserNotifications(1, 50, 0); err != nil || len(page) != 3 {
		t.Fatalf("warm cache: len=%d err=%v", len(page), err)
	}

	if err := manager.MarkAllAsRead(1); err != nil {
		t.Fatalf("mark all as read: %v", err)
	}

	var unread int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = 1 AND read = false`).Scan(&unread); err != nil {
		t.Fatalf("count unread notifications: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread notifications = %d, want 0", unread)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = 2 AND read = false`).Scan(&unread); err != nil {
		t.Fatalf("count other user unread notifications: %v", err)
	}
	if unread != 1 {
		t.Fatalf("other user unread notifications = %d, want 1", unread)
	}

	page, err := manager.GetUserNotifications(1, 50, 0)
	if err != nil {
		t.Fatalf("read cached notifications: %v", err)
	}
	for _, notification := range page {
		if !notification.Read {
			t.Fatalf("cached notification %d is unread", notification.ID)
		}
	}
}
