//go:build test

package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/integrations/todoist"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// todoistLinkScanCounter classifies todoist_task_links queries so tests can
// assert the sync never issues the old unbounded per-user link scan and that
// chunked lookups really page through a large set.
type todoistLinkScanCounter struct {
	database.Database
	mu               sync.Mutex
	unboundedScans   int
	itemChunkQueries int
	tdLookups        int
	sweepPages       int
}

func (db *todoistLinkScanCounter) Query(query string, args ...any) (*sql.Rows, error) {
	db.classify(query)
	return db.Database.Query(query, args...)
}

func (db *todoistLinkScanCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.classify(query)
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *todoistLinkScanCounter) classify(query string) {
	compact := strings.Join(strings.Fields(query), " ")
	if !strings.Contains(compact, "FROM todoist_task_links") || !strings.Contains(compact, "user_id = ?") {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	switch {
	case strings.Contains(compact, "IN (") && strings.Contains(compact, "item_id IN"):
		db.itemChunkQueries++
	case strings.Contains(compact, "IN ("):
		db.tdLookups++
	case strings.Contains(compact, "todoist_task_id > ?"):
		db.sweepPages++
	default:
		db.unboundedScans++
	}
}

func newSyncHarnessWithDB(t *testing.T, wrap func(database.Database) database.Database) (*TodoistSyncService, database.Database) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	wrapped := tdb.Database
	if wrap != nil {
		wrapped = wrap(tdb.Database)
	}
	svc := &TodoistSyncService{db: wrapped, syncRepo: repository.NewTodoistSyncRepository(wrapped)}
	return svc, tdb.Database
}

// A large link set must be reconciled through bounded, chunked queries — the
// delta lookups, per-chunk workspace-task mappings, and the keyset orphan
// sweep — never through one unbounded per-user scan. All pairs stay in sync,
// the orphan is deleted, and chunk boundaries lose no links.
func TestReconcileHandlesLargeLinkSetWithBoundedQueries(t *testing.T) {
	const linkedPairs = 600 // spans more than one 500-link chunk

	counter := &todoistLinkScanCounter{}
	svc, rawDB := newSyncHarnessWithDB(t, func(db database.Database) database.Database {
		counter.Database = db
		return counter
	})
	store := newFakeStore()
	api := &fakeTodoistAPI{} // empty delta: Todoist unchanged

	for i := 0; i < linkedPairs; i++ {
		itemID := 1000 + i
		tdID := fmt.Sprintf("td-%04d", i)
		st := taskState{Title: fmt.Sprintf("Task %d", i), Description: "body"}
		store.tasks[itemID] = st
		seedLink(t, svc, itemID, tdID, st)
	}
	// One orphan: the Windshift copy is gone, so the sweep must delete it.
	seedLink(t, svc, 99999, "td-orphan", taskState{Title: "Orphan"})

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.DeletedInTD != 1 {
		t.Fatalf("deleted in Todoist = %d, want the 1 orphan", stats.DeletedInTD)
	}
	if deletes := api.commandsOfType("item_delete"); len(deletes) != 1 {
		t.Fatalf("delete commands = %d, want 1", len(deletes))
	}
	if adds := api.commandsOfType("item_add"); len(adds) != 0 {
		t.Fatalf("add commands = %d, want 0; every task already has a link", len(adds))
	}

	var liveLinks int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM todoist_task_links WHERE user_id = '5'`).Scan(&liveLinks); err != nil {
		t.Fatalf("count remaining links: %v", err)
	}
	if liveLinks != linkedPairs {
		t.Fatalf("remaining links = %d, want %d (orphan link must be deleted)", liveLinks, linkedPairs)
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.unboundedScans != 0 {
		t.Fatalf("reconcile issued %d unbounded per-user link scans, want 0", counter.unboundedScans)
	}
	if counter.itemChunkQueries < 2 {
		t.Fatalf("workspace-task link lookups = %d queries; a %d-link set must span multiple chunks", counter.itemChunkQueries, linkedPairs)
	}
	if counter.sweepPages == 0 {
		t.Fatal("orphan sweep issued no keyset page queries")
	}
}

// A link created during the delta pass (new Todoist task) must not be picked
// up as an orphan by the same run's sweep even though the stale task snapshot
// predates it.
func TestReconcileKeepsLinksCreatedDuringDeltaPass(t *testing.T) {
	svc, rawDB := newSyncHarnessWithDB(t, nil)
	store := newFakeStore()
	api := &fakeTodoistAPI{delta: &todoist.SyncResponse{
		SyncToken: "next",
		Items:     []todoist.Item{{ID: "td-new-1", ProjectID: "p1", Content: "New todoist task"}},
	}}

	stats, _, err := svc.reconcile(baseConfig(), api, store)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.CreatedInWS != 1 {
		t.Fatalf("created in workspace = %d, want 1", stats.CreatedInWS)
	}
	var links int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM todoist_task_links WHERE user_id = '5'`).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 1 {
		t.Fatalf("links after run = %d, want the 1 new link", links)
	}
	if deletes := api.commandsOfType("item_delete"); len(deletes) != 0 {
		t.Fatalf("delete commands = %d, want 0; the fresh link must survive the sweep", len(deletes))
	}
}
