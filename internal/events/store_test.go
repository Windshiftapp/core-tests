//go:build test

package events

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestAppendUsesSourceTransactionAndSequencesAggregate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)

	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	rolledBack, err := store.Append(ctx, tx, testEvent("item", "42", "item.created"))
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if rolledBack.AggregateSequence != 1 {
		t.Fatalf("rolled-back sequence = %d, want 1", rolledBack.AggregateSequence)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	assertRowCount(t, tdb, "domain_events", 0)
	assertRowCount(t, tdb, "domain_event_streams", 0)

	first, err := store.AppendStandalone(ctx, testEvent("item", "42", "item.created"))
	if err != nil {
		t.Fatalf("first AppendStandalone() error = %v", err)
	}
	secondInput := testEvent("item", "42", "item.updated")
	secondInput.CorrelationID = "request-7"
	secondInput.CausationEventKey = first.Key
	second, err := store.AppendStandalone(ctx, secondInput)
	if err != nil {
		t.Fatalf("second AppendStandalone() error = %v", err)
	}
	other, err := store.AppendStandalone(ctx, testEvent("item", "99", "item.created"))
	if err != nil {
		t.Fatalf("other AppendStandalone() error = %v", err)
	}

	if first.AggregateSequence != 1 || second.AggregateSequence != 2 || other.AggregateSequence != 1 {
		t.Fatalf("aggregate sequences = %d, %d, %d; want 1, 2, 1", first.AggregateSequence, second.AggregateSequence, other.AggregateSequence)
	}
	loaded, err := store.Event(ctx, second.ID)
	if err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	if loaded.Key != second.Key || loaded.CorrelationID != "request-7" || loaded.CausationEventKey != first.Key || string(loaded.Payload) != `{"value":1}` {
		t.Fatalf("loaded event = %+v", loaded)
	}
}

func TestAppendBatchAllocatesDistinctAggregateSequencesSetBased(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)

	first := appendEvent(t, store, testEvent("item", "42", "item.created"))
	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	batch, err := store.AppendBatch(ctx, tx, []NewEvent{
		testEvent("item", "42", "item.updated"),
		testEvent("item", "99", "item.created"),
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit batch: %v", err)
	}
	if len(batch) != 2 || batch[0].AggregateID != "42" || batch[0].AggregateSequence != 2 || batch[1].AggregateID != "99" || batch[1].AggregateSequence != 1 {
		t.Fatalf("batch = %+v, want input order with sequences 2 and 1", batch)
	}
	if batch[0].ID <= first.ID || batch[1].ID <= batch[0].ID {
		t.Fatalf("batch IDs = %d, %d after first %d", batch[0].ID, batch[1].ID, first.ID)
	}

	tx, err = tdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin duplicate transaction: %v", err)
	}
	_, err = store.AppendBatch(ctx, tx, []NewEvent{
		testEvent("item", "7", "item.created"),
		testEvent("item", "7", "item.updated"),
	})
	if err == nil {
		t.Fatal("AppendBatch() accepted two events for one aggregate")
	}
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		t.Fatalf("rollback duplicate transaction: %v", rollbackErr)
	}
	assertRowCount(t, tdb, "domain_events", 3)
}

func TestAppendBatchChunksWithinDatabaseParameterLimits(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	inputs := make([]NewEvent, maxAppendBatchSize+1)
	for i := range inputs {
		inputs[i] = testEvent("item", strconv.Itoa(i+1), "item.updated")
	}
	tx, err := tdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	events, err := NewStore(tdb.DB).AppendBatch(ctx, tx, inputs)
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(events) != len(inputs) || events[0].AggregateID != "1" || events[len(events)-1].AggregateID != strconv.Itoa(len(inputs)) {
		t.Fatalf("events = %d, first/last = %s/%s", len(events), events[0].AggregateID, events[len(events)-1].AggregateID)
	}
	assertRowCount(t, tdb, "domain_events", len(inputs))
	assertRowCount(t, tdb, "domain_event_streams", len(inputs))
}

func TestDeliveryPreservesPerAggregateOrderWithoutSerializingOtherAggregates(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection.items", "item.changed")

	first := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	second := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	other := appendEvent(t, store, testEvent("item", "99", "item.changed"))
	reconcile(t, store, 3)
	now := time.Now().UTC().Add(time.Second)

	firstClaim := claim(t, store, "projection.items", "worker-1", now)
	if firstClaim.Event.ID != first.ID {
		t.Fatalf("first claim event = %d, want %d", firstClaim.Event.ID, first.ID)
	}
	otherClaim := claim(t, store, "projection.items", "worker-2", now)
	if otherClaim.Event.ID != other.ID {
		t.Fatalf("parallel claim event = %d, want unrelated aggregate event %d", otherClaim.Event.ID, other.ID)
	}
	if err := store.Complete(ctx, otherClaim, now); err != nil {
		t.Fatalf("complete unrelated aggregate: %v", err)
	}
	blocked, err := store.Claim(ctx, "projection.items", "worker-2", now, time.Minute)
	if err != nil {
		t.Fatalf("blocked Claim() error = %v", err)
	}
	if blocked != nil {
		t.Fatalf("blocked Claim() = event %d, want nil", blocked.Event.ID)
	}
	if err := store.Complete(ctx, firstClaim, now); err != nil {
		t.Fatalf("complete first aggregate event: %v", err)
	}
	secondClaim := claim(t, store, "projection.items", "worker-2", now)
	if secondClaim.Event.ID != second.ID {
		t.Fatalf("second aggregate claim event = %d, want %d", secondClaim.Event.ID, second.ID)
	}
}

func TestExpiredLeaseIsReclaimedAndRejectsStaleAcknowledgement(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "search.index", "item.changed")
	event := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	reconcile(t, store, 1)
	now := time.Now().UTC().Add(time.Second)

	stale := claim(t, store, "search.index", "worker-old", now)
	reclaimed := claim(t, store, "search.index", "worker-new", now.Add(2*time.Minute))
	if stale.Event.ID != event.ID || reclaimed.Event.ID != event.ID {
		t.Fatalf("claimed event IDs = %d and %d, want %d", stale.Event.ID, reclaimed.Event.ID, event.ID)
	}
	if stale.LeaseToken == reclaimed.LeaseToken {
		t.Fatal("reclaimed delivery reused the stale lease token")
	}
	if err := store.Complete(ctx, stale, now.Add(2*time.Minute)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale Complete() error = %v, want ErrLeaseLost", err)
	}
	if err := store.Complete(ctx, reclaimed, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("reclaimed Complete() error = %v", err)
	}
}

func TestFailedDeliveryReplayAndSkipAreAuditedAndUnblockStream(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "notifications", "item.changed")
	first := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	second := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	reconcile(t, store, 2)
	now := time.Now().UTC().Add(time.Second)
	operator := Operator{Kind: "user", Ref: "7"}

	delivery := claim(t, store, "notifications", "worker-1", now)
	if err := store.Fail(ctx, delivery, errors.New("invalid target"), false, now); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	blocked, err := store.Claim(ctx, "notifications", "worker-2", now, time.Minute)
	if err != nil {
		t.Fatalf("blocked Claim() error = %v", err)
	}
	if blocked != nil {
		t.Fatalf("blocked Claim() = event %d, want nil", blocked.Event.ID)
	}
	if err := store.Replay(ctx, first.ID, "notifications", operator, "target repaired", now); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	replayed := claim(t, store, "notifications", "worker-2", now)
	if replayed.AttemptCount != 1 {
		t.Fatalf("replayed attempt count = %d, want reset count 1", replayed.AttemptCount)
	}
	if err := store.Fail(ctx, replayed, errors.New("still invalid"), false, now); err != nil {
		t.Fatalf("second Fail() error = %v", err)
	}
	if err := store.Skip(ctx, first.ID, "notifications", operator, "obsolete notification", now); err != nil {
		t.Fatalf("Skip() error = %v", err)
	}
	next := claim(t, store, "notifications", "worker-3", now)
	if next.Event.ID != second.ID {
		t.Fatalf("claim after skip = event %d, want %d", next.Event.ID, second.ID)
	}

	rows, err := tdb.Query(`
		SELECT action, operator_kind, operator_ref, reason
		FROM domain_event_delivery_actions
		WHERE event_id = ? AND consumer_key = ?
		ORDER BY id
	`, first.ID, "notifications")
	if err != nil {
		t.Fatalf("query audit actions: %v", err)
	}
	defer rows.Close()
	want := []struct{ action, reason string }{{"replay", "target repaired"}, {"skip", "obsolete notification"}}
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("audit action %d missing", index)
		}
		var action, kind, ref, reason string
		if err := rows.Scan(&action, &kind, &ref, &reason); err != nil {
			t.Fatalf("scan audit action %d: %v", index, err)
		}
		if action != expected.action || reason != expected.reason || kind != "user" || ref != "7" {
			t.Fatalf("audit action %d = %q/%q/%q/%q", index, action, kind, ref, reason)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra audit action")
	}
}

func TestConsumerContractCannotChangeAfterDeliveryBegins(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.created")

	if err := store.ConfigureConsumer(ctx, Consumer{
		Key: "projection", HandlerVersion: 2, Active: true, StartEventID: 1,
		EventTypes: []string{"item.changed"},
	}); err != nil {
		t.Fatalf("change unused consumer contract: %v", err)
	}
	appendEvent(t, store, testEvent("item", "42", "item.changed"))
	reconcile(t, store, 1)

	err := store.ConfigureConsumer(ctx, Consumer{
		Key: "projection", HandlerVersion: 3, Active: true, StartEventID: 1,
		EventTypes: []string{"item.changed", "item.deleted"},
	})
	if !errors.Is(err, ErrConsumerContract) {
		t.Fatalf("ConfigureConsumer() error = %v, want ErrConsumerContract", err)
	}
	if err := store.ConfigureConsumer(ctx, Consumer{
		Key: "projection", HandlerVersion: 3, Active: false, StartEventID: 1,
		EventTypes: []string{"item.changed"},
	}); err != nil {
		t.Fatalf("update handler version and activation with stable contract: %v", err)
	}
}

func TestPruneKeepsCheckpointAndDoesNotRecreateDelivery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "analytics", "item.changed")
	event := appendEvent(t, store, testEvent("item", "42", "item.changed"))
	reconcile(t, store, 1)
	now := time.Now().UTC().Add(time.Second)
	delivery := claim(t, store, "analytics", "worker", now)
	if err := store.Complete(ctx, delivery, now); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	result, err := store.Prune(ctx, now.Add(time.Hour), event.RecordedAt.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("first Prune() error = %v", err)
	}
	if result.Deliveries != 1 || result.Events != 0 {
		t.Fatalf("first Prune() = %+v, want 1 delivery and 0 events", result)
	}
	created, err := store.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("Reconcile() after delivery prune error = %v", err)
	}
	if created != 0 {
		t.Fatalf("Reconcile() recreated %d deliveries, want 0", created)
	}

	result, err = store.Prune(ctx, now.Add(time.Hour), event.RecordedAt.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("second Prune() error = %v", err)
	}
	if result.Events != 1 {
		t.Fatalf("second Prune() = %+v, want 1 event", result)
	}
}

func TestEngineProcessesRegisteredConsumerAndReportsStats(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	engine := NewEngine(tdb.DB, DefaultConfig())
	configureConsumer(t, engine.Store(), "projection", "item.changed")
	event := appendEvent(t, engine.Store(), testEvent("item", "42", "item.changed"))
	reconcile(t, engine.Store(), 1)

	var handled Event
	if err := engine.RegisterHandler("projection", HandlerFunc(func(_ context.Context, event Event) error {
		handled = event
		return nil
	})); err != nil {
		t.Fatalf("RegisterHandler() error = %v", err)
	}
	engine.now = func() time.Time { return time.Now().UTC().Add(time.Second) }
	worked, err := engine.processOne(ctx)
	if err != nil {
		t.Fatalf("processOne() error = %v", err)
	}
	if !worked || handled.ID != event.ID {
		t.Fatalf("processOne() worked=%t handled=%d, want true/%d", worked, handled.ID, event.ID)
	}
	stats, err := engine.Store().Stats(ctx, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if len(stats) != 1 || stats[0].Completed != 1 || stats[0].Pending != 0 || stats[0].Failed != 0 {
		t.Fatalf("Stats() = %+v", stats)
	}
}

func TestEngineShutdownCanBeAwaitedAgainAfterTimeout(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	config := Config{
		WorkerCount: 1, PollInterval: 5 * time.Millisecond,
		LeaseDuration: time.Second, HandlerTimeout: 500 * time.Millisecond,
		ReconcileBatch: 10, MaxAttempts: 2,
		BaseRetryDelay: time.Millisecond, MaxRetryDelay: time.Second,
	}
	engine := NewEngine(tdb.DB, config)
	configureConsumer(t, engine.Store(), "blocking", "item.changed")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	if err := engine.RegisterHandler("blocking", HandlerFunc(func(context.Context, Event) error {
		started <- struct{}{}
		<-release
		return nil
	})); err != nil {
		t.Fatalf("RegisterHandler() error = %v", err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := engine.AppendStandalone(context.Background(), testEvent("item", "42", "item.changed")); err != nil {
		t.Fatalf("AppendStandalone() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}

	timedOut, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Shutdown(timedOut); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Shutdown() error = %v, want context cancellation", err)
	}
	close(release)
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := engine.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if err := engine.Start(context.Background()); err == nil {
		t.Fatal("Start() after shutdown succeeded")
	}
	if err := engine.RegisterHandler("late", HandlerFunc(func(context.Context, Event) error { return nil })); err == nil {
		t.Fatal("RegisterHandler() after startup succeeded")
	}
}

func testEvent(aggregateType, aggregateID, eventType string) NewEvent {
	return NewEvent{
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		Type:           eventType,
		PayloadVersion: 1,
		ActorKind:      "user",
		ActorRef:       "7",
		SourceKind:     "api",
		SourceRef:      "request-7",
		Payload:        []byte(`{"value":1}`),
	}
}

func configureConsumer(t *testing.T, store *Store, key string, eventTypes ...string) {
	t.Helper()
	if err := store.ConfigureConsumer(context.Background(), Consumer{
		Key: key, HandlerVersion: 1, Active: true, StartEventID: 1, EventTypes: eventTypes,
	}); err != nil {
		t.Fatalf("ConfigureConsumer(%q) error = %v", key, err)
	}
}

func appendEvent(t *testing.T, store *Store, input NewEvent) *Event {
	t.Helper()
	event, err := store.AppendStandalone(context.Background(), input)
	if err != nil {
		t.Fatalf("AppendStandalone(%s/%s) error = %v", input.AggregateType, input.AggregateID, err)
	}
	return event
}

func reconcile(t *testing.T, store *Store, want int64) {
	t.Helper()
	created, err := store.Reconcile(context.Background(), 100)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if created != want {
		t.Fatalf("Reconcile() created %d deliveries, want %d", created, want)
	}
}

func claim(t *testing.T, store *Store, consumerKey, owner string, now time.Time) Delivery {
	t.Helper()
	delivery, err := store.Claim(context.Background(), consumerKey, owner, now, time.Minute)
	if err != nil {
		t.Fatalf("Claim(%q, %q) error = %v", consumerKey, owner, err)
	}
	if delivery == nil {
		t.Fatalf("Claim(%q, %q) = nil", consumerKey, owner)
	}
	return *delivery
}

func assertRowCount(t *testing.T, db database.Database, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", table, got, want)
	}
}
