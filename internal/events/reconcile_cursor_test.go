//go:build test

package events

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func TestReconcileConsumerCreatesDeliveriesPastCursor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")

	appendEvent(t, store, testEvent("item", "1", "item.changed"))
	second := appendEvent(t, store, testEvent("item", "2", "item.changed"))
	third := appendEvent(t, store, testEvent("item", "3", "item.changed"))

	maxSeen := gateMaxEventID(t, store)
	created, err := store.reconcileConsumer(ctx, "projection", second.ID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() error = %v", err)
	}
	if created != 1 || maxSeen != third.ID {
		t.Fatalf("reconcileConsumer() = (%d, gate max %d), want (1, %d)", created, maxSeen, third.ID)
	}
	delivery := claim(t, store, "projection", "worker", time.Now().UTC().Add(time.Second))
	if delivery.Event.ID != third.ID {
		t.Fatalf("claimed event = %d, want %d (only the event past the cursor)", delivery.Event.ID, third.ID)
	}

	created, err = store.reconcileConsumer(ctx, "projection", maxSeen, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() past maxSeen error = %v", err)
	}
	if created != 0 || gateMaxEventID(t, store) != third.ID {
		t.Fatalf("reconcileConsumer() past maxSeen created %d, want 0", created)
	}
}

func TestReconcileConsumerCursorAdvancesAcrossNewEvents(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")

	first := appendEvent(t, store, testEvent("item", "1", "item.changed"))
	second := appendEvent(t, store, testEvent("item", "2", "item.changed"))

	maxSeen := gateMaxEventID(t, store)
	created, err := store.reconcileConsumer(ctx, "projection", 0, time.Time{}, 10)
	if err != nil {
		t.Fatalf("bootstrap reconcileConsumer() error = %v", err)
	}
	if created != 2 || maxSeen != second.ID {
		t.Fatalf("bootstrap reconcileConsumer() = (%d, gate max %d), want (2, %d)", created, maxSeen, second.ID)
	}

	third := appendEvent(t, store, testEvent("item", "3", "item.changed"))
	maxSeen = gateMaxEventID(t, store)
	created, err = store.reconcileConsumer(ctx, "projection", second.ID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("incremental reconcileConsumer() error = %v", err)
	}
	if created != 1 || maxSeen != third.ID {
		t.Fatalf("incremental reconcileConsumer() = (%d, gate max %d), want (1, %d)", created, maxSeen, third.ID)
	}
	if first.ID >= second.ID || second.ID >= third.ID {
		t.Fatalf("event ids %d, %d, %d are not ascending", first.ID, second.ID, third.ID)
	}
}

func TestReconcileConsumerRespectsStartEventIDPastCursor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	if err := store.ConfigureConsumer(ctx, Consumer{
		Key: "projection", HandlerVersion: 1, Active: true,
		StartEventID: 2, EventTypes: []string{"item.changed"},
	}); err != nil {
		t.Fatalf("ConfigureConsumer() error = %v", err)
	}

	appendEvent(t, store, testEvent("item", "1", "item.changed"))
	second := appendEvent(t, store, testEvent("item", "2", "item.changed"))
	third := appendEvent(t, store, testEvent("item", "3", "item.changed"))

	maxSeen := gateMaxEventID(t, store)
	created, err := store.reconcileConsumer(ctx, "projection", 0, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() error = %v", err)
	}
	if created != 2 || maxSeen != third.ID {
		t.Fatalf("reconcileConsumer() = (%d, gate max %d), want (2, %d) excluding the event before start_event_id", created, maxSeen, third.ID)
	}
	claimOne := claim(t, store, "projection", "worker", time.Now().UTC().Add(time.Second))
	claimTwo := claim(t, store, "projection", "worker", time.Now().UTC().Add(time.Second))
	if claimOne.Event.ID != second.ID || claimTwo.Event.ID != third.ID {
		t.Fatalf("claimed events = %d, %d; want %d, %d", claimOne.Event.ID, claimTwo.Event.ID, second.ID, third.ID)
	}
}

func TestReconcileConsumerSkipsInactiveAndUnknownConsumers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "inactive", "item.changed")

	event := appendEvent(t, store, testEvent("item", "1", "item.changed"))
	if err := store.SetConsumerActive(ctx, "inactive", false); err != nil {
		t.Fatalf("SetConsumerActive() error = %v", err)
	}

	maxSeen := gateMaxEventID(t, store)
	created, err := store.reconcileConsumer(ctx, "inactive", 0, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() inactive error = %v", err)
	}
	if created != 0 || maxSeen != event.ID {
		t.Fatalf("reconcileConsumer() inactive = (%d, gate max %d), want (0, %d)", created, maxSeen, event.ID)
	}

	created, err = store.reconcileConsumer(ctx, "ghost", 0, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() unknown error = %v", err)
	}
	if created != 0 || gateMaxEventID(t, store) != event.ID {
		t.Fatalf("reconcileConsumer() unknown created %d, want 0", created)
	}

	if err := store.SetConsumerActive(ctx, "inactive", true); err != nil {
		t.Fatalf("reactivate SetConsumerActive() error = %v", err)
	}
	created, err = store.reconcileConsumer(ctx, "inactive", 0, time.Time{}, 10)
	if err != nil {
		t.Fatalf("reconcileConsumer() reactivated error = %v", err)
	}
	if created != 1 {
		t.Fatalf("reconcileConsumer() reactivated = %d, want 1", created)
	}
}

func TestReconcileLimitAppliesAcrossConsumers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "a-consumer", "item.changed")
	configureConsumer(t, store, "b-consumer", "item.changed")
	appendEvent(t, store, testEvent("item", "1", "item.changed"))
	appendEvent(t, store, testEvent("item", "2", "item.changed"))

	created, err := store.Reconcile(ctx, 3)
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if created != 3 {
		t.Fatalf("first Reconcile() created %d, want 3", created)
	}
	created, err = store.Reconcile(ctx, 3)
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if created != 1 {
		t.Fatalf("second Reconcile() created %d, want 1", created)
	}
	created, err = store.Reconcile(ctx, 3)
	if err != nil {
		t.Fatalf("third Reconcile() error = %v", err)
	}
	if created != 0 {
		t.Fatalf("third Reconcile() created %d, want 0", created)
	}
	assertRowCount(t, tdb, "domain_event_deliveries", 4)
}

func TestEngineReconcileRecoversLateEventWithinLookback(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	config := Config{
		WorkerCount: 1, PollInterval: time.Millisecond,
		LeaseDuration: time.Second, HandlerTimeout: 500 * time.Millisecond,
		ReconcileBatch: 10, MaxAttempts: 2,
		BaseRetryDelay: time.Millisecond, MaxRetryDelay: time.Second,
		SafetyReconcileInterval: time.Minute, ReconcileLookback: 10 * time.Minute,
	}
	engine := NewEngine(tdb.DB, config)
	configureConsumer(t, engine.Store(), "projection", "item.changed")

	now := time.Now().UTC().Truncate(time.Second)
	engine.now = func() time.Time { return now }

	appendEvent(t, engine.Store(), testEvent("item", "1", "item.changed"))
	second := appendEvent(t, engine.Store(), testEvent("item", "2", "item.changed"))

	created, err := engine.tick(ctx)
	if err != nil {
		t.Fatalf("first tick() error = %v", err)
	}
	if created != 2 {
		t.Fatalf("first tick() created %d, want 2", created)
	}
	if engine.reconcileCursors["projection"] != second.ID {
		t.Fatalf("cursor = %d, want %d", engine.reconcileCursors["projection"], second.ID)
	}

	// Simulate an event whose inserting transaction committed after the
	// cursor advanced past its (lower) id: the incremental scan must skip it.
	third := appendEvent(t, engine.Store(), testEvent("item", "3", "item.changed"))
	engine.reconcileCursors["projection"] = third.ID + 100
	now = now.Add(time.Second)
	created, err = engine.tick(ctx)
	if err != nil {
		t.Fatalf("incremental tick() error = %v", err)
	}
	if created != 0 {
		t.Fatalf("incremental tick() created %d, want 0", created)
	}
	assertRowCount(t, tdb, "domain_event_deliveries", 2)

	// The bounded safety rescan recovers the skipped event because it was
	// recorded inside the lookback window.
	now = now.Add(2 * time.Minute)
	created, err = engine.tick(ctx)
	if err != nil {
		t.Fatalf("safety tick() error = %v", err)
	}
	if created != 1 {
		t.Fatalf("safety tick() created %d, want 1", created)
	}
	assertRowCount(t, tdb, "domain_event_deliveries", 3)
}

// WI-1770: the safety rescan is bounded by the lookback window, not by the
// total retained history.
func TestReconcileConsumerWindowBoundsScannedEvents(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")

	const oldEvents = 500
	for i := 0; i < oldEvents; i++ {
		appendEvent(t, store, testEvent("item", fmt.Sprintf("old-%d", i), "item.changed"))
	}
	recent := appendEvent(t, store, testEvent("item", "recent", "item.changed"))
	if _, err := tdb.Exec(`UPDATE domain_events SET recorded_at = ? WHERE id <> ?`,
		time.Now().UTC().Add(-48*time.Hour), recent.ID); err != nil {
		t.Fatalf("age old events: %v", err)
	}

	created, err := store.reconcileConsumer(ctx, "projection", 0, time.Now().UTC().Add(-5*time.Minute), 250)
	if err != nil {
		t.Fatalf("windowed reconcileConsumer() error = %v", err)
	}
	if created != 1 {
		t.Fatalf("windowed reconcileConsumer() created %d, want 1 (only the recent event)", created)
	}
	var deliveredEventID int64
	if err := tdb.QueryRow(`SELECT event_id FROM domain_event_deliveries`).Scan(&deliveredEventID); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if deliveredEventID != recent.ID {
		t.Fatalf("delivered event = %d, want the recent event %d", deliveredEventID, recent.ID)
	}
}

// The window filter must let the planner use idx_domain_events_recorded_at
// rather than scanning the full retained history.
func TestReconcileConsumerWindowQueryUsesRecordedAtIndex(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	if tdb.Engine != "sqlite" {
		t.Skip("SQLite query-plan regression")
	}
	rows, err := tdb.DB.Query("EXPLAIN QUERY PLAN "+reconcileConsumerQuery("sqlite", true),
		0, "projection", time.Now(), 250)
	if err != nil {
		t.Fatalf("explain reconcile query: %v", err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	if !strings.Contains(plan, "idx_domain_events_recorded_at") {
		t.Fatalf("reconcile window plan does not use the recorded_at index:\n%s", plan)
	}
}

func TestEngineReconcileCursorDropsDeletedConsumers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	config := Config{
		WorkerCount: 1, PollInterval: time.Millisecond,
		LeaseDuration: time.Second, HandlerTimeout: 500 * time.Millisecond,
		ReconcileBatch: 10, MaxAttempts: 2,
		BaseRetryDelay: time.Millisecond, MaxRetryDelay: time.Second,
		SafetyReconcileInterval: time.Hour, ReconcileLookback: time.Hour,
	}
	engine := NewEngine(tdb.DB, config)
	configureConsumer(t, engine.Store(), "projection", "item.changed")
	appendEvent(t, engine.Store(), testEvent("item", "1", "item.changed"))

	if _, err := engine.tick(ctx); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if _, ok := engine.reconcileCursors["projection"]; !ok {
		t.Fatal("cursor for active consumer missing after reconcile")
	}

	if _, err := engine.Store().db.ExecContext(ctx, `DELETE FROM domain_event_deliveries`); err != nil {
		t.Fatalf("clear deliveries: %v", err)
	}
	if _, err := engine.Store().db.ExecContext(ctx, `DELETE FROM domain_event_consumers WHERE consumer_key = 'projection'`); err != nil {
		t.Fatalf("delete consumer: %v", err)
	}
	if _, err := engine.tick(ctx); err != nil {
		t.Fatalf("tick() after consumer deletion error = %v", err)
	}
	if _, ok := engine.reconcileCursors["projection"]; ok {
		t.Fatal("cursor kept for deleted consumer")
	}
}
