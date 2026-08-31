//go:build test

package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestDiagnosticsFilterReportsWorkspaceQueueHealth(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection.items", "item.changed")
	now := time.Now().UTC().Add(time.Minute)
	workspaceOne := 101
	workspaceTwo := 202

	pendingInput := testEvent("item", "pending", "item.changed")
	pendingInput.WorkspaceID = &workspaceOne
	pending := appendEvent(t, store, pendingInput)
	retryInput := testEvent("item", "retry", "item.changed")
	retryInput.WorkspaceID = &workspaceOne
	retrying := appendEvent(t, store, retryInput)
	failedInput := testEvent("item", "failed", "item.changed")
	failedInput.WorkspaceID = &workspaceOne
	failed := appendEvent(t, store, failedInput)
	activeLeaseInput := testEvent("item", "active-lease", "item.changed")
	activeLeaseInput.WorkspaceID = &workspaceOne
	activeLease := appendEvent(t, store, activeLeaseInput)
	expiredLeaseInput := testEvent("item", "expired-lease", "item.changed")
	expiredLeaseInput.WorkspaceID = &workspaceTwo
	expiredLease := appendEvent(t, store, expiredLeaseInput)
	reconcile(t, store, 5)

	retryDelivery := claim(t, store, "projection.items", "retry-worker", now)
	if retryDelivery.Event.ID != pending.ID {
		t.Fatalf("first claim event = %d, want oldest pending %d", retryDelivery.Event.ID, pending.ID)
	}
	if err := store.Fail(ctx, retryDelivery, errors.New("temporary"), true, now.Add(time.Hour)); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	failedDelivery := claim(t, store, "projection.items", "failed-worker", now)
	if failedDelivery.Event.ID != retrying.ID {
		t.Fatalf("second claim event = %d, want %d", failedDelivery.Event.ID, retrying.ID)
	}
	if err := store.Fail(ctx, failedDelivery, errors.New("terminal"), false, now); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	activeDelivery := claim(t, store, "projection.items", "active-worker", now)
	if activeDelivery.Event.ID != failed.ID {
		t.Fatalf("third claim event = %d, want %d", activeDelivery.Event.ID, failed.ID)
	}
	expiredDelivery := claim(t, store, "projection.items", "expired-worker", now)
	if expiredDelivery.Event.ID != activeLease.ID {
		t.Fatalf("fourth claim event = %d, want %d", expiredDelivery.Event.ID, activeLease.ID)
	}
	otherWorkspaceDelivery := claim(t, store, "projection.items", "other-worker", now)
	if otherWorkspaceDelivery.Event.ID != expiredLease.ID {
		t.Fatalf("fifth claim event = %d, want %d", otherWorkspaceDelivery.Event.ID, expiredLease.ID)
	}
	if _, err := tdb.Exec(`
		UPDATE domain_event_deliveries
		SET lease_expires_at = ?
		WHERE event_id IN (?, ?)
	`, now.Add(-time.Minute), expiredDelivery.Event.ID, otherWorkspaceDelivery.Event.ID); err != nil {
		t.Fatalf("expire diagnostic leases: %v", err)
	}

	stats, err := store.StatsFiltered(ctx, DiagnosticsFilter{
		ConsumerKey: "projection.items",
		WorkspaceID: &workspaceOne,
	}, now)
	if err != nil {
		t.Fatalf("StatsFiltered() error = %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("StatsFiltered() returned %d consumers, want 1", len(stats))
	}
	got := stats[0]
	if got.Pending != 0 || got.Retrying != 1 || got.RetryAttempts != 1 || got.Failed != 1 ||
		got.ActiveLeases != 1 || got.ExpiredLeases != 1 || got.BlockedAggregates != 1 {
		t.Fatalf("workspace diagnostics = %+v", got)
	}
	if got.Leased != 2 || got.OldestPendingAt == nil || got.OldestPendingAge <= 0 {
		t.Fatalf("lease/age diagnostics = %+v", got)
	}

	failures, err := store.FailedDeliveries(ctx, DiagnosticsFilter{WorkspaceID: &workspaceOne}, 10)
	if err != nil {
		t.Fatalf("FailedDeliveries() error = %v", err)
	}
	if len(failures) != 1 || failures[0].EventID != retrying.ID || failures[0].LastError != "terminal" {
		t.Fatalf("workspace failures = %+v", failures)
	}

	result, err := store.Prune(ctx, now.Add(24*time.Hour), now.Add(24*time.Hour), 100)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.Deliveries != 0 || result.Events != 0 {
		t.Fatalf("Prune() removed live work: %+v", result)
	}
}

func TestReplayAuditUsesSuppliedOperatorTimestamp(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection.items", "item.changed")
	event := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	reconcile(t, store, 1)
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	delivery := claim(t, store, "projection.items", "worker", now)
	if err := store.Fail(ctx, delivery, errors.New("terminal"), false, now); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if err := store.Replay(ctx, event.ID, "projection.items", Operator{Kind: "user", Ref: "77"}, "configuration repaired", now); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	var kind, ref, reason string
	var createdAt time.Time
	if err := tdb.QueryRow(`
		SELECT operator_kind, operator_ref, reason, created_at
		FROM domain_event_delivery_actions
		WHERE event_id = ? AND consumer_key = ?
	`, event.ID, "projection.items").Scan(&kind, &ref, &reason, &createdAt); err != nil {
		t.Fatalf("read replay audit: %v", err)
	}
	if kind != "user" || ref != "77" || reason != "configuration repaired" || !createdAt.Equal(now) {
		t.Fatalf("replay audit = %q/%q/%q/%s", kind, ref, reason, createdAt)
	}
}

func TestEngineRetentionUsesIndependentDeliveryAndEventWindows(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	config := DefaultConfig()
	config.CompletedDeliveryRetention = time.Hour
	config.EventRetention = 24 * time.Hour
	config.RetentionBatch = 10
	engine := NewEngine(tdb.DB, config)
	configureConsumer(t, engine.Store(), "projection.items", "item.changed")
	event := appendEvent(t, engine.Store(), testEvent("item", "42", "item.changed"))
	reconcile(t, engine.Store(), 1)
	delivery := claim(t, engine.Store(), "projection.items", "worker", event.RecordedAt.Add(time.Minute))
	if err := engine.Store().Complete(ctx, delivery, event.RecordedAt.Add(time.Minute)); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	engine.now = func() time.Time { return event.RecordedAt.Add(2 * time.Hour) }
	result, err := engine.pruneOnce(ctx)
	if err != nil {
		t.Fatalf("first pruneOnce() error = %v", err)
	}
	if result.Deliveries != 1 || result.Events != 0 {
		t.Fatalf("first pruneOnce() = %+v, want delivery only", result)
	}

	engine.now = func() time.Time { return event.RecordedAt.Add(25 * time.Hour) }
	result, err = engine.pruneOnce(ctx)
	if err != nil {
		t.Fatalf("second pruneOnce() error = %v", err)
	}
	if result.Deliveries != 0 || result.Events != 1 {
		t.Fatalf("second pruneOnce() = %+v, want event only", result)
	}
}
