package webhook

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

type countingPluginDispatcher struct {
	calls atomic.Int64
}

type blockingPluginDispatcher struct {
	active  atomic.Int64
	maximum atomic.Int64
	release <-chan struct{}
	started chan<- struct{}
}

func (d *blockingPluginDispatcher) DispatchToPlugin(ctx context.Context, _, _, _ string, _ json.RawMessage) error {
	active := d.active.Add(1)
	defer d.active.Add(-1)
	for {
		maximum := d.maximum.Load()
		if active <= maximum || d.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	d.started <- struct{}{}
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *countingPluginDispatcher) DispatchToPlugin(context.Context, string, string, string, json.RawMessage) error {
	d.calls.Add(1)
	return nil
}

func TestDispatchEventBoundsQueueAndWorkers(t *testing.T) {
	dispatchCtx, dispatchCancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	w := &WebhookSender{
		dispatchCtx:    dispatchCtx,
		dispatchCancel: dispatchCancel,
		dispatchQueue:  make(chan dispatchJob, 2),
		accepting:      true,
		dispatchJobFn: func(dispatchJob) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		},
	}
	w.startDispatchWorkers(1)

	item := &models.Item{ID: 42}
	w.DispatchEvent("item.updated", item)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dispatch worker did not start")
	}

	w.DispatchEvent("item.updated", item)
	w.DispatchEvent("item.updated", item)
	w.DispatchEvent("item.updated", item)

	stats := w.Stats()
	if stats.ActiveWorkers != 1 {
		t.Fatalf("active workers = %d, want 1", stats.ActiveWorkers)
	}
	if stats.QueueDepth != 2 || stats.QueueCapacity != 2 {
		t.Fatalf("queue = %d/%d, want 2/2", stats.QueueDepth, stats.QueueCapacity)
	}
	if stats.Enqueued != 3 || stats.Rejected != 1 {
		t.Fatalf("admission counters = %d enqueued, %d rejected; want 3 and 1", stats.Enqueued, stats.Rejected)
	}
	if stats.OldestEventAgeMillis < 0 {
		t.Fatalf("oldest event age = %dms, want non-negative", stats.OldestEventAgeMillis)
	}

	close(release)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	w.DispatchEvent("item.updated", item)
	if rejected := w.Stats().Rejected; rejected != 2 {
		t.Fatalf("rejected after shutdown = %d, want 2", rejected)
	}
	if stats := w.Stats(); stats.Processed != 3 || stats.OldestEventAgeMillis != 0 {
		t.Fatalf("drained stats = processed %d, oldest %dms; want 3 and 0", stats.Processed, stats.OldestEventAgeMillis)
	}
}

func TestSubscriptionIndexCachesAndInvalidates(t *testing.T) {
	db, err := database.NewSQLiteDBWithPoolSizes("file:webhook-subscription-index?mode=memory&cache=shared", 2, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}

	config := models.ChannelConfig{
		WebhookAutoTrigger:      true,
		WebhookScopeType:        "all",
		WebhookSubscribedEvents: []string{"item.updated"},
		WebhookURL:              "https://example.com/hook",
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	result, err := db.ExecWrite(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('indexed', 'webhook', 'outbound', 'enabled', ?)
	`, string(configJSON))
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	channelID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("channel id: %v", err)
	}

	sender := NewWebhookSender(db)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sender.Shutdown(ctx)
	})
	item := &models.Item{ID: 42, WorkspaceID: 7}

	for range 2 {
		matches, err := sender.GetMatchingWebhooks(context.Background(), "item.updated", item)
		if err != nil {
			t.Fatalf("match cached subscriptions: %v", err)
		}
		if len(matches) != 1 || matches[0].ChannelID != int(channelID) {
			t.Fatalf("matches = %#v, want channel %d", matches, channelID)
		}
	}

	config.WebhookSubscribedEvents = []string{"item.created"}
	configJSON, _ = json.Marshal(config)
	if _, err := db.ExecWrite("UPDATE channels SET config = ? WHERE id = ?", string(configJSON), channelID); err != nil {
		t.Fatalf("update channel config: %v", err)
	}
	sender.InvalidateSubscriptions()

	updatedMatches, err := sender.GetMatchingWebhooks(context.Background(), "item.updated", item)
	if err != nil {
		t.Fatalf("match invalidated subscriptions: %v", err)
	}
	createdMatches, err := sender.GetMatchingWebhooks(context.Background(), "item.created", item)
	if err != nil {
		t.Fatalf("match reloaded subscriptions: %v", err)
	}
	if len(updatedMatches) != 0 || len(createdMatches) != 1 {
		t.Fatalf("matches after invalidation = updated %d, created %d; want 0 and 1", len(updatedMatches), len(createdMatches))
	}

	stats := sender.Stats()
	if stats.SubscriptionCacheEntries != 1 || stats.SubscriptionCacheMisses != 2 || stats.SubscriptionCacheHits < 2 || stats.SubscriptionInvalidations != 1 {
		t.Fatalf("subscription stats = %#v", stats)
	}
}

func TestDispatchHydratesPayloadOnceForMatchingDestinations(t *testing.T) {
	db, err := database.NewSQLiteDBWithPoolSizes("file:webhook-payload-reuse?mode=memory&cache=shared", 2, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}

	configJSON, err := json.Marshal(models.ChannelConfig{
		WebhookAutoTrigger:      true,
		WebhookScopeType:        "all",
		WebhookSubscribedEvents: []string{"item.updated"},
		WebhookPluginHandler:    "onWebhook",
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	for _, name := range []string{"plugin-one", "plugin-two"} {
		if _, err := db.ExecWrite(`
			INSERT INTO channels (name, type, direction, status, config, plugin_name, plugin_webhook_id)
			VALUES (?, 'webhook', 'outbound', 'enabled', ?, 'test-plugin', ?)
		`, name, string(configJSON), name); err != nil {
			t.Fatalf("insert channel %s: %v", name, err)
		}
	}

	sender := NewWebhookSender(db)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sender.Shutdown(ctx)
	})
	dispatcher := &countingPluginDispatcher{}
	sender.SetPluginDispatcher(dispatcher)
	var payloadLoads atomic.Int64
	sender.itemPayloadFn = func(context.Context, string, *models.Item) (json.RawMessage, error) {
		payloadLoads.Add(1)
		return json.RawMessage(`{"id":42}`), nil
	}

	sender.DispatchEvent("item.updated", &models.Item{ID: 42, WorkspaceID: 7})
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sender.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown after dispatch: %v", err)
	}

	stats := sender.Stats()
	if stats.Processed != 1 || payloadLoads.Load() != 1 || dispatcher.calls.Load() != 2 || stats.DeliveryCount != 2 {
		t.Fatalf("dispatch stats = %#v, payload loads = %d, plugin calls = %d", stats, payloadLoads.Load(), dispatcher.calls.Load())
	}
}

func TestDeliveryConcurrencyIsBoundedPerDestination(t *testing.T) {
	db, err := database.NewSQLiteDBWithPoolSizes("file:webhook-destination-limit?mode=memory&cache=shared", 2, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize database: %v", err)
	}

	sender := NewWebhookSender(db)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sender.Shutdown(ctx)
	})
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	dispatcher := &blockingPluginDispatcher{release: release, started: started}
	sender.SetPluginDispatcher(dispatcher)
	webhook := WebhookConfig{ChannelID: 99, PluginName: "test-plugin", PluginHandler: "onWebhook"}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sender.sendWebhookPayload(context.Background(), webhook, "item.updated", 42, json.RawMessage(`{"id":42}`))
		}()
	}
	for range destinationConcurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("destination deliveries did not reach the concurrency limit")
		}
	}
	if maximum := dispatcher.maximum.Load(); maximum != destinationConcurrency {
		t.Fatalf("maximum destination concurrency = %d, want %d", maximum, destinationConcurrency)
	}
	close(release)
	wg.Wait()
}
