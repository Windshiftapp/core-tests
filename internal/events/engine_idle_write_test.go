//go:build test

package events

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

type writeCountingDB struct {
	database.Database
	writes atomic.Int64
}

func (c *writeCountingDB) Begin() (database.Tx, error) {
	c.writes.Add(1)
	return c.Database.Begin()
}

func (c *writeCountingDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	c.writes.Add(1)
	return c.Database.BeginTx(ctx, opts)
}

func (c *writeCountingDB) ExecWrite(query string, args ...any) (sql.Result, error) {
	c.writes.Add(1)
	return c.Database.ExecWrite(query, args...)
}

func (c *writeCountingDB) ExecWriteContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.writes.Add(1)
	return c.Database.ExecWriteContext(ctx, query, args...)
}

// TestIdleEngineWorkersDoNotTouchTheWriteConnection covers WI-1771: with no
// pending deliveries, idle workers must not open claim transactions.
func TestIdleEngineWorkersDoNotTouchTheWriteConnection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	counting := &writeCountingDB{Database: tdb.DB}
	engine := NewEngine(counting, DefaultConfig())
	configureConsumer(t, engine.Store(), "projection", "item.changed")

	if created, err := engine.tick(ctx); err != nil || created != 0 {
		t.Fatalf("idle tick() = (%d, %v), want (0, nil)", created, err)
	}
	before := counting.writes.Load()
	for i := 0; i < 8; i++ {
		worked, err := engine.processOne(ctx)
		if err != nil {
			t.Fatalf("idle processOne() error = %v", err)
		}
		if worked {
			t.Fatal("idle processOne() reported work")
		}
	}
	if got := counting.writes.Load(); got != before {
		t.Fatalf("idle workers opened %d write transactions, want 0", got-before)
	}
}
