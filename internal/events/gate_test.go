//go:build test

package events

import (
	"context"
	"database/sql"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// countingDB counts read statements so a tick's cost can be budgeted.
type countingDB struct {
	database.Database
	queries atomic.Int64
}

func (c *countingDB) Query(query string, args ...any) (*sql.Rows, error) {
	c.queries.Add(1)
	return c.Database.Query(query, args...)
}

func (c *countingDB) QueryRow(query string, args ...any) *sql.Row {
	c.queries.Add(1)
	return c.Database.QueryRow(query, args...)
}

func (c *countingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.queries.Add(1)
	return c.Database.QueryContext(ctx, query, args...)
}

func (c *countingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.queries.Add(1)
	return c.Database.QueryRowContext(ctx, query, args...)
}

// gateMaxEventID is the value the engine stores as a consumer cursor after an
// exhaustive scan.
func gateMaxEventID(t *testing.T, store *Store) int64 {
	t.Helper()
	gate, err := store.Gate(context.Background(), 0, time.Now().UTC())
	if err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	return gate.MaxEventID
}

func TestGateReportsActiveConsumersAndNoWorkWhenIdle(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")

	gate, err := store.Gate(ctx, 0, time.Now().UTC())
	if err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if gate.MaxEventID != 0 {
		t.Fatalf("MaxEventID = %d, want 0", gate.MaxEventID)
	}
	if len(gate.ScanKeys) != 0 {
		t.Fatalf("ScanKeys = %v, want none", gate.ScanKeys)
	}
	if len(gate.ClaimKeys) != 0 {
		t.Fatalf("ClaimKeys = %v, want none", gate.ClaimKeys)
	}
	if !slices.Equal(gate.ActiveKeys, []string{"projection"}) {
		t.Fatalf("ActiveKeys = %v, want [projection]", gate.ActiveKeys)
	}
}

func TestGateNamesScansOnlyWhileEventsSitPastTheCursor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")
	first := appendEvent(t, store, testEvent("item", "1", "item.changed"))

	gate, err := store.Gate(ctx, 0, time.Now().UTC())
	if err != nil {
		t.Fatalf("Gate() at cursor 0 error = %v", err)
	}
	if !slices.Equal(gate.ScanKeys, []string{"projection"}) {
		t.Fatalf("ScanKeys at cursor 0 = %v, want [projection]", gate.ScanKeys)
	}
	if gate.MaxEventID != first.ID {
		t.Fatalf("MaxEventID = %d, want %d", gate.MaxEventID, first.ID)
	}

	gate, err = store.Gate(ctx, first.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Gate() at cursor %d error = %v", first.ID, err)
	}
	if len(gate.ScanKeys) != 0 {
		t.Fatalf("ScanKeys at cursor %d = %v, want none", first.ID, gate.ScanKeys)
	}

	second := appendEvent(t, store, testEvent("item", "2", "item.changed"))
	gate, err = store.Gate(ctx, first.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Gate() after append error = %v", err)
	}
	if !slices.Equal(gate.ScanKeys, []string{"projection"}) {
		t.Fatalf("ScanKeys after append = %v, want [projection]", gate.ScanKeys)
	}
	if gate.MaxEventID != second.ID {
		t.Fatalf("MaxEventID = %d, want %d", gate.MaxEventID, second.ID)
	}
}

func TestGateNamesClaimsOnlyWhenADeliveryIsDue(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")
	event := appendEvent(t, store, testEvent("item", "1", "item.changed"))
	reconcile(t, store, 1)

	now := time.Now().UTC()
	gate, err := store.Gate(ctx, event.ID, now)
	if err != nil {
		t.Fatalf("Gate() with a due delivery error = %v", err)
	}
	if !slices.Equal(gate.ClaimKeys, []string{"projection"}) {
		t.Fatalf("ClaimKeys with a due delivery = %v, want [projection]", gate.ClaimKeys)
	}

	claim(t, store, "projection", "worker", now)
	gate, err = store.Gate(ctx, event.ID, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Gate() during the lease error = %v", err)
	}
	if len(gate.ClaimKeys) != 0 {
		t.Fatalf("ClaimKeys during the lease = %v, want none", gate.ClaimKeys)
	}

	gate, err = store.Gate(ctx, event.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Gate() after the lease expired error = %v", err)
	}
	if !slices.Equal(gate.ClaimKeys, []string{"projection"}) {
		t.Fatalf("ClaimKeys after the lease expired = %v, want [projection]", gate.ClaimKeys)
	}
}

func TestGateHoldsRetryDeliveriesUntilTheirNextAttempt(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	store := NewStore(tdb.DB)
	configureConsumer(t, store, "projection", "item.changed")
	event := appendEvent(t, store, testEvent("item", "1", "item.changed"))
	reconcile(t, store, 1)

	now := time.Now().UTC()
	retryAt := now.Add(time.Hour)
	if _, err := tdb.ExecContext(ctx, `
		UPDATE domain_event_deliveries
		SET state = 'retry', next_attempt_at = ?
		WHERE event_id = ? AND consumer_key = 'projection'
	`, retryAt, event.ID); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}

	gate, err := store.Gate(ctx, event.ID, now)
	if err != nil {
		t.Fatalf("Gate() before the retry is due error = %v", err)
	}
	if len(gate.ClaimKeys) != 0 {
		t.Fatalf("ClaimKeys before the retry is due = %v, want none", gate.ClaimKeys)
	}

	gate, err = store.Gate(ctx, event.ID, retryAt)
	if err != nil {
		t.Fatalf("Gate() when the retry is due error = %v", err)
	}
	if !slices.Equal(gate.ClaimKeys, []string{"projection"}) {
		t.Fatalf("ClaimKeys when the retry is due = %v, want [projection]", gate.ClaimKeys)
	}
}

func TestTickPrunesCursorWhenConsumerGoesInactive(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	engine := NewEngine(tdb.DB, DefaultConfig())
	configureConsumer(t, engine.Store(), "projection", "item.changed")
	event := appendEvent(t, engine.Store(), testEvent("item", "1", "item.changed"))

	if created, err := engine.tick(ctx); err != nil || created != 1 {
		t.Fatalf("tick() = (%d, %v), want (1, nil)", created, err)
	}
	if engine.reconcileCursors["projection"] != event.ID {
		t.Fatalf("cursor = %d, want %d", engine.reconcileCursors["projection"], event.ID)
	}

	if err := engine.Store().SetConsumerActive(ctx, "projection", false); err != nil {
		t.Fatalf("SetConsumerActive() error = %v", err)
	}
	if created, err := engine.tick(ctx); err != nil || created != 0 {
		t.Fatalf("idle tick() = (%d, %v), want (0, nil)", created, err)
	}
	if _, ok := engine.reconcileCursors["projection"]; ok {
		t.Fatal("cursor kept for a consumer that went inactive")
	}
}

func TestTickCostsOneStatementWhenIdle(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	counting := &countingDB{Database: tdb.DB}
	engine := NewEngine(counting, DefaultConfig())
	consumers := []string{"projection", "notifier", "search"}
	for _, key := range consumers {
		configureConsumer(t, engine.Store(), key, "item.changed")
	}
	appendEvent(t, engine.Store(), testEvent("item", "1", "item.changed"))
	if created, err := engine.tick(ctx); err != nil || created != int64(len(consumers)) {
		t.Fatalf("tick() = (%d, %v), want (%d, nil)", created, err, len(consumers))
	}
	for _, key := range consumers {
		delivery := claim(t, engine.Store(), key, "worker", time.Now().UTC())
		if err := engine.Store().Complete(ctx, delivery, time.Now().UTC()); err != nil {
			t.Fatalf("Complete(%q) error = %v", key, err)
		}
	}

	counting.queries.Store(0)
	if created, err := engine.tick(ctx); err != nil || created != 0 {
		t.Fatalf("idle tick() = (%d, %v), want (0, nil)", created, err)
	}
	if got := counting.queries.Load(); got != 1 {
		t.Fatalf("idle tick issued %d read statements, want 1 for %d consumers", got, len(consumers))
	}
}
