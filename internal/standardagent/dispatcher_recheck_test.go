//go:build test

package standardagent

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

// standardQueueQueryCounter classifies the queries the dispatcher issues so
// tests can assert the worker-exit recheck targets one queue key instead of
// scanning the whole agent_runs table.
type standardQueueQueryCounter struct {
	database.Database
	mu            sync.Mutex
	distinctScans int
	existsChecks  int
}

func (db *standardQueueQueryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.classify(query)
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *standardQueueQueryCounter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.classify(query)
	return db.Database.QueryRowContext(ctx, query, args...)
}

func (db *standardQueueQueryCounter) classify(query string) {
	compact := strings.Join(strings.Fields(query), " ")
	db.mu.Lock()
	defer db.mu.Unlock()
	if strings.Contains(compact, "FROM agent_runs") && strings.Contains(compact, "SELECT DISTINCT binding_id, item_id") {
		db.distinctScans++
	}
	if strings.Contains(compact, "SELECT EXISTS") && strings.Contains(compact, "FROM agent_runs") {
		db.existsChecks++
	}
}

// A Standard worker exit must recheck its own queue with a targeted existence
// check, never the whole-table DISTINCT that startup resume uses.
func TestDispatcherWorkerExitRecheckTargetsQueueKey(t *testing.T) {
	counter := &standardQueueQueryCounter{}
	dispatcher, _, _, _, itemID := newStandardRuntimeWithDB(t, fixedResolver{client: finalAnswerClient{answer: "done"}},
		func(db database.Database) database.Database {
			counter.Database = db
			return counter
		})

	// No queued run: the drain claims nothing, exits, and runs the recheck once.
	dispatcher.kick(repository.StandardQueueKey{BindingID: 71, ItemID: itemID})
	dispatcher.wg.Wait()

	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.distinctScans != 0 {
		t.Fatalf("worker exit issued %d whole-table DISTINCT queue scans, want 0", counter.distinctScans)
	}
	if counter.existsChecks != 1 {
		t.Fatalf("worker exit issued %d targeted existence checks, want 1", counter.existsChecks)
	}
}
