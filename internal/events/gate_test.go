//go:build test

package events

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// countingDatabase counts statement executions so the idle-tick budget can be
// asserted. It wraps the test database, which also proves the gate works on the
// engine the suite is running against.
type countingDatabase struct {
	database.Database
	statements   int
	writes       int
	transactions int
	queries      []string
}

func (db *countingDatabase) record(kind, query string) {
	db.statements++
	if kind == "write" {
		db.writes++
	}
	db.queries = append(db.queries, kind+" "+query)
}

func (db *countingDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.record("read", query)
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *countingDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.record("read", query)
	return db.Database.QueryRowContext(ctx, query, args...)
}

func (db *countingDatabase) ExecWriteContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db.record("write", query)
	return db.Database.ExecWriteContext(ctx, query, args...)
}

func (db *countingDatabase) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	db.transactions++
	return db.Database.BeginTx(ctx, opts)
}

func (db *countingDatabase) reset() {
	db.statements = 0
	db.writes = 0
	db.transactions = 0
	db.queries = nil
}

func gateConfig() Config {
	config := DefaultConfig()
	config.WorkerCount = 1
	config.RetentionInterval = 0
	return config
}

// An idle tick must cost one statement and open no transaction. Before the gate
// an idle tick cost one statement per consumer plus one transaction per claim,
// which is what burned CPU on an empty queue in production.
func TestIdleTickCostsOneStatementAndNoTransaction(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	counter := &countingDatabase{Database: tdb}
	engine := NewEngine(counter, gateConfig())
	store := NewStore(counter)
	if err := engine.RegisterHandler("gate.consumer", HandlerFunc(func(_ context.Context, _ Event) error { return nil })); err != nil {
		t.Fatal(err)
	}
	configureConsumer(t, store, "gate.consumer", "item.created")
	appendEvent(t, store, testEvent("item", "1", "item.created"))

	// The first tick is the history backstop, so it still runs one scan per
	// consumer the gate names. Nothing is scanned for a consumer that does not
	// exist or has no events past its cursor.
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counter.statements != 2 {
		t.Fatalf("first tick executed %d statements, want gate plus one scan: %v", counter.statements, counter.queries)
	}
	if counter.writes != 1 {
		t.Fatalf("first tick executed %d writes, want the reconcile insert", counter.writes)
	}

	counter.reset()
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counter.statements != 1 {
		t.Fatalf("idle tick executed %d statements, want 1 gate statement: %v", counter.statements, counter.queries)
	}
	if counter.writes != 0 {
		t.Fatalf("idle tick executed %d writes, want 0", counter.writes)
	}
	if counter.transactions != 0 {
		t.Fatalf("idle tick opened %d transactions, want 0", counter.transactions)
	}
}

// A worker must not open a transaction when the gate reported no work.
func TestIdleProcessOneClaimsNothing(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	counter := &countingDatabase{Database: tdb}
	engine := NewEngine(counter, gateConfig())
	if err := engine.RegisterHandler("gate.consumer", HandlerFunc(func(_ context.Context, _ Event) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	counter.reset()
	worked, err := engine.processOne(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if worked {
		t.Fatal("processOne claimed work the gate did not report")
	}
	if counter.statements != 0 {
		t.Fatalf("processOne executed %d statements with no reported work, want 0", counter.statements)
	}
}

// Work found by the gate is reconciled without the per-consumer MAX(id) read,
// and the cursor advances from the gate's highest visible event id.
func TestGateFindsWorkWithoutPerConsumerMaxQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	counter := &countingDatabase{Database: tdb}
	engine := NewEngine(counter, gateConfig())
	store := NewStore(counter)
	handled := 0
	if err := engine.RegisterHandler("gate.consumer", HandlerFunc(func(_ context.Context, _ Event) error {
		handled++
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureConsumer(context.Background(), Consumer{
		Key:            "gate.consumer",
		HandlerVersion: 1,
		Active:         true,
		StartEventID:   1,
		EventTypes:     []string{"item.created"},
	}); err != nil {
		t.Fatal(err)
	}
	event, err := store.AppendStandalone(context.Background(), testEvent("item", "42", "item.created"))
	if err != nil {
		t.Fatal(err)
	}

	counter.reset()
	created, err := engine.tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("tick created %d deliveries, want 1", created)
	}
	// Gate plus the scan. The per-consumer MAX(id) read the gate replaced must
	// not run, so exactly two statements are allowed.
	if counter.statements != 2 {
		t.Fatalf("tick executed %d statements, want gate plus scan: %v", counter.statements, counter.queries)
	}
	if engine.reconcileCursors["gate.consumer"] != event.ID {
		t.Fatalf("cursor = %d, want event id %d", engine.reconcileCursors["gate.consumer"], event.ID)
	}
	if !strings.Contains(strings.Join(engine.workKeys, ","), "gate.consumer") {
		t.Fatalf("gate did not report the consumer with work: %v", engine.workKeys)
	}

	worked, err := engine.processOne(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !worked || handled != 1 {
		t.Fatalf("processOne handled %d events, want 1", handled)
	}

	// With the queue empty the engine returns to the one-statement budget.
	counter.reset()
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counter.statements != 1 {
		t.Fatalf("tick after work executed %d statements, want 1 gate statement: %v", counter.statements, counter.queries)
	}
}

// An inactive consumer must not be scanned on idle ticks. Only the periodic full
// scan touches it, which matches the ActiveConsumerKeys behaviour the gate
// replaced.
func TestInactiveConsumerIsNotScannedOnIdleTicks(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	counter := &countingDatabase{Database: tdb}
	engine := NewEngine(counter, gateConfig())
	if err := engine.RegisterHandler("inactive.consumer", HandlerFunc(func(_ context.Context, _ Event) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(counter).ConfigureConsumer(context.Background(), Consumer{
		Key:            "inactive.consumer",
		HandlerVersion: 1,
		Active:         false,
		StartEventID:   1,
		EventTypes:     []string{"item.created"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	counter.reset()
	if _, err := engine.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counter.statements != 1 {
		t.Fatalf("idle tick executed %d statements for an inactive consumer, want 1: %v", counter.statements, counter.queries)
	}
	if counter.writes != 0 {
		t.Fatalf("idle tick wrote %d statements for an inactive consumer, want 0", counter.writes)
	}
}

// The scan still covers every active consumer, not only consumers with a handler
// in this process: deliveries must exist for a handler registered later. The gate
// could have been narrowed to registered handlers, which would have broken this,
// so the behavior is pinned here.
func TestActiveConsumerWithoutHandlerIsStillScanned(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	store := NewStore(tdb)
	configureConsumer(t, store, "unhandled.consumer", "item.created")
	appendEvent(t, store, testEvent("item", "7", "item.created"))

	engine := NewEngine(tdb, gateConfig())
	created, err := engine.tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("tick created %d deliveries for an active consumer with no handler, want 1", created)
	}
	if keys := engine.eligibleHandlerKeys(); len(keys) != 0 {
		t.Fatalf("eligibleHandlerKeys() = %v, want none before a handler is registered", keys)
	}

	// A handler registered later finds the deliveries the engine already created.
	if err := engine.RegisterHandler("unhandled.consumer", HandlerFunc(func(_ context.Context, _ Event) error {
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if keys := engine.eligibleHandlerKeys(); len(keys) != 1 || keys[0] != "unhandled.consumer" {
		t.Fatalf("eligibleHandlerKeys() = %v, want the consumer the gate named", keys)
	}
}

// Eligibility covers two ranges: pending/retry by next_attempt_at and leased by
// lease_expires_at. The gate runs them as separate indexed branches, so a
// regression that drops one branch leaves expired leases unclaimed until the
// safety reconcile. Pin both branches, and pin that a delivery scheduled for
// the future is not named.
func TestGateNamesDueStatesOnly(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	store := NewStore(tdb)
	now := time.Now().UTC().Truncate(time.Second)

	for _, key := range []string{"due.lease", "due.pending", "future.pending"} {
		configureConsumer(t, store, key, "item.created")
	}
	event := appendEvent(t, store, testEvent("item", "9", "item.created"))

	insert := func(key, state string, nextAttempt time.Time, leaseExpiry any) {
		t.Helper()
		if _, err := tdb.ExecWrite(
			`INSERT INTO domain_event_deliveries (event_id, consumer_key, state, next_attempt_at, lease_expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			event.ID, key, state, nextAttempt, leaseExpiry); err != nil {
			t.Fatalf("insert %s delivery for %s: %v", state, key, err)
		}
	}
	insert("due.lease", "leased", now, now.Add(-time.Minute))
	insert("due.pending", "pending", now.Add(-time.Minute), nil)
	insert("future.pending", "pending", now.Add(time.Hour), nil)

	result, err := store.Gate(context.Background(), event.ID+1, now)
	if err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	slices.Sort(result.ClaimKeys)
	if want := []string{"due.lease", "due.pending"}; !slices.Equal(result.ClaimKeys, want) {
		t.Fatalf("ClaimKeys = %v, want %v", result.ClaimKeys, want)
	}
	if len(result.ScanKeys) != 0 {
		t.Fatalf("ScanKeys = %v, want none when every cursor is current", result.ScanKeys)
	}
}

// The gate must stay dialect-neutral: no array parameters, no locking clause,
// plain placeholders rewritten by the database layer. SQLite has no locking
// clause and no ON CONFLICT DO NOTHING, so both branches must survive.
func TestGateQueryIsDialectNeutral(t *testing.T) {
	if strings.Contains(gateSQL, "ANY(") || strings.Contains(gateSQL, "FOR UPDATE") {
		t.Fatalf("gate statement is not dialect-neutral: %s", gateSQL)
	}
	if !strings.Contains(gateSQL, "?") || strings.Contains(gateSQL, "$1") {
		t.Fatalf("gate statement must use plain placeholders: %s", gateSQL)
	}
}

func TestDialectBranchesStaySeparated(t *testing.T) {
	if strings.Contains(claimQuery("sqlite"), "FOR UPDATE") {
		t.Fatalf("SQLite claim must not carry a locking clause: %s", claimQuery("sqlite"))
	}
	if !strings.Contains(claimQuery("postgres"), "FOR UPDATE OF delivery SKIP LOCKED") {
		t.Fatalf("PostgreSQL claim lost its locking clause: %s", claimQuery("postgres"))
	}
	if !strings.Contains(reconcileConsumerQuery("sqlite", false), "INSERT OR IGNORE") {
		t.Fatalf("SQLite reconcile lost INSERT OR IGNORE: %s", reconcileConsumerQuery("sqlite", false))
	}
	if strings.Contains(reconcileConsumerQuery("sqlite", false), "ON CONFLICT") ||
		!strings.Contains(reconcileConsumerQuery("postgres", true), "ON CONFLICT (event_id, consumer_key) DO NOTHING") {
		t.Fatalf("reconcile dialect branch is wrong: sqlite=%s postgres=%s", reconcileConsumerQuery("sqlite", false), reconcileConsumerQuery("postgres", true))
	}
}
