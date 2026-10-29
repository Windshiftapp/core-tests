//go:build test

package sla

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
)

// countingDB counts read queries through both the database and its
// transactions so the inline evaluation path can be budgeted.
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

func (c *countingDB) Begin() (database.Tx, error) {
	tx, err := c.Database.Begin()
	if err != nil {
		return nil, err
	}
	return &countingTx{Tx: tx, counter: &c.queries}, nil
}

type countingTx struct {
	database.Tx
	counter *atomic.Int64
}

func (t *countingTx) Query(query string, args ...any) (*sql.Rows, error) {
	t.counter.Add(1)
	return t.Tx.Query(query, args...)
}

func (t *countingTx) QueryRow(query string, args ...any) *sql.Row {
	t.counter.Add(1)
	return t.Tx.QueryRow(query, args...)
}

func (t *countingTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	t.counter.Add(1)
	return t.Tx.QueryContext(ctx, query, args...)
}

func (t *countingTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	t.counter.Add(1)
	return t.Tx.QueryRowContext(ctx, query, args...)
}

// TestIrrelevantChangeIssuesAtMostOneQuery guards §14: a write that no metric
// input set references must cost only the workspace gate.
func TestIrrelevantChangeIssuesAtMostOneQuery(t *testing.T) {
	fixture := newEngineFixture(t)
	// The goal references a real field, so the metric's input set is bounded and
	// a rank drag stays irrelevant (a constant goal widens to all fields).
	fixture.createMetric(t,
		[]models.SLACondition{
			statusCondition(models.SLAPhaseStart, fixture.statusOpen),
			statusCondition(models.SLAPhaseStop, fixture.statusDone),
		},
		[]models.SLAGoal{{
			Position: 0, QLQuery: "priority = 'SLA High'", ImportStatus: "native",
			Targets: []models.SLAGoalTarget{{Position: 0, TargetMs: 3_600_000, CalendarID: fixture.calendarID}},
		}},
	)

	counting := &countingDB{Database: fixture.db}
	engine := NewEngine(counting)
	engine.SetNudge(func(time.Time) {})

	observe := func(fact itemevents.RecordedFact) {
		t.Helper()
		if err := database.WithTx(counting, func(tx database.Tx) error {
			return engine.ObserveItemFacts(context.Background(), tx, []itemevents.RecordedFact{fact})
		}); err != nil {
			t.Fatalf("ObserveItemFacts: %v", err)
		}
	}

	// Warm the compiled-config cache and any cycle state with a relevant fact.
	observe(fixture.createdFact())
	counting.queries.Store(0)

	// A rank drag touches no field the metric references.
	irrelevant := itemevents.RecordedFact{
		Type: itemevents.Updated, ItemID: fixture.itemID, WorkspaceID: fixture.workspaceID,
		Snapshot: itemevents.ItemSnapshot{ID: fixture.itemID, WorkspaceID: fixture.workspaceID, StatusID: &fixture.statusOpen, PriorityID: &fixture.priorityAPI},
		Changes:  []itemevents.FieldChange{{Field: "frac_index", OldValue: "a", NewValue: "b"}},
		Metadata: itemevents.Metadata{ActorKind: "user"},
	}
	observe(irrelevant)

	if got := counting.queries.Load(); got != 1 {
		t.Fatalf("irrelevant-change path issued %d read queries, want exactly the workspace gate (1)", got)
	}
}
