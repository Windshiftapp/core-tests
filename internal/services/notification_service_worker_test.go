package services

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/models"
)

type recordingNotificationManager struct {
	mu      sync.Mutex
	batches [][]models.Notification
}

func (m *recordingNotificationManager) AddNotification(notification models.Notification) (models.Notification, error) {
	stored, err := m.AddNotifications([]models.Notification{notification})
	return stored[0], err
}

func (m *recordingNotificationManager) AddNotifications(notifications []models.Notification) ([]models.Notification, error) {
	return m.AddNotificationsContext(context.Background(), notifications)
}

func (m *recordingNotificationManager) AddNotificationsContext(_ context.Context, notifications []models.Notification) ([]models.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copyOfBatch := append([]models.Notification(nil), notifications...)
	m.batches = append(m.batches, copyOfBatch)
	return copyOfBatch, nil
}

func (m *recordingNotificationManager) DeleteUserNotifications(int) error       { return nil }
func (m *recordingNotificationManager) MarkNotificationsSent([]int) error       { return nil }
func (m *recordingNotificationManager) MarkNotificationsSendFailed([]int) error { return nil }
func (m *recordingNotificationManager) RollbackNotificationsSent([]int) error   { return nil }

func newNotificationWorkerHarness(
	workers, queueSize int,
	process func(context.Context, *NotificationEvent) error,
) *NotificationService {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	service := &NotificationService{
		config: NotificationServiceConfig{
			EventWorkers:        workers,
			EventBufferSize:     queueSize,
			EventProcessTimeout: time.Hour,
			ShutdownTimeout:     time.Second,
		},
		eventChan:      make(chan queuedNotificationEvent, queueSize),
		cacheStop:      make(chan struct{}),
		ruleCache:      &RuleCache{LastRefreshed: time.Now()},
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
		processEventFn: process,
	}
	service.wg.Add(workers)
	for workerID := 0; workerID < workers; workerID++ {
		go service.eventProcessor(workerID)
	}
	return service
}

func TestNotificationServiceBoundsQueueAndWorkerConcurrency(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var active, maxActive atomic.Int64
	process := func(ctx context.Context, _ *NotificationEvent) error {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		active.Add(-1)
		return nil
	}
	service := newNotificationWorkerHarness(2, 2, process)

	for i := 0; i < 2; i++ {
		if !service.TryEmitEvent(&NotificationEvent{EventType: "test"}) {
			t.Fatal("initial event rejected")
		}
	}
	<-started
	<-started
	if !service.TryEmitEvent(&NotificationEvent{EventType: "queued-1"}) ||
		!service.TryEmitEvent(&NotificationEvent{EventType: "queued-2"}) {
		t.Fatal("event rejected within queue capacity")
	}
	if service.TryEmitEvent(&NotificationEvent{EventType: "overflow"}) {
		t.Fatal("event accepted beyond queue capacity")
	}
	stats := service.GetStats()
	if stats["pending_events"] != 2 || stats["events_dropped"] != 1 || stats["max_active_workers"] != 2 {
		t.Fatalf("notification worker stats = %+v", stats)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.CloseContext(ctx); err != nil {
		t.Fatalf("close notification service: %v", err)
	}
	if maxActive.Load() > 2 {
		t.Fatalf("processor concurrency = %d", maxActive.Load())
	}
}

func TestNotificationServiceNotifyUsersUsesOneRecipientBatch(t *testing.T) {
	manager := &recordingNotificationManager{}
	service := &NotificationService{notificationManager: manager}
	if err := service.NotifyUsers([]int{1, 2, 2, 3, 0}, 4, 5, 2, "info", "title", "message"); err != nil {
		t.Fatalf("notify users: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.batches) != 1 || len(manager.batches[0]) != 2 {
		t.Fatalf("notification batches = %+v", manager.batches)
	}
	if manager.batches[0][0].UserID != 1 || manager.batches[0][1].UserID != 3 {
		t.Fatalf("recipient batch = %+v", manager.batches[0])
	}
}

func TestNotificationServiceShutdownDeadlineReportsUndrainedWork(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	process := func(ctx context.Context, _ *NotificationEvent) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}
	service := newNotificationWorkerHarness(1, 2, process)
	service.TryEmitEvent(&NotificationEvent{EventType: "active"})
	service.TryEmitEvent(&NotificationEvent{EventType: "queued"})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.CloseContext(ctx); err == nil {
		t.Fatal("shutdown deadline returned nil")
	}
	workersDone := make(chan struct{})
	go func() {
		service.wg.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-time.After(time.Second):
		t.Fatal("notification worker did not stop after cancellation")
	}
	if active := service.GetStats()["active_workers"]; active != 0 {
		t.Fatalf("active workers after cancellation = %d", active)
	}
}
