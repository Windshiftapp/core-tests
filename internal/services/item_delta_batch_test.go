//go:build test

package services

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// deltaReadCountingDB counts every read routed through the database handle.
type deltaReadCountingDB struct {
	database.Database
	reads int
}

func (db *deltaReadCountingDB) Query(query string, args ...any) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func (db *deltaReadCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.reads++
	return db.Database.QueryContext(ctx, query, args...)
}

func (db *deltaReadCountingDB) QueryRow(query string, args ...any) *sql.Row {
	db.reads++
	return db.Database.QueryRow(query, args...)
}

func (db *deltaReadCountingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.reads++
	return db.Database.QueryRowContext(ctx, query, args...)
}

// TestItemChangesResolvesVisibilityInOneBatch guards the delta N+1: visibility
// for every changed item must resolve in a single list read, not one list
// pipeline per item, because sustained activity can carry hundreds of entries.
func TestItemChangesResolvesVisibilityInOneBatch(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	base := tdb.GetDatabase()

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permissions, err := NewPermissionService(base, config)
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })

	newApp := func(db database.Database) *ItemApplicationService {
		return NewItemApplicationService(db, permissions,
			NewItemCreationService(db, permissions),
			NewItemUpdateApplicationService(db, permissions),
			NewItemDeletionApplicationService(db, permissions),
		)
	}
	application := newApp(base)
	actor := AuditActor{UserID: data.UserID, Username: "testuser"}
	ctx := context.Background()

	// Capture the baseline watermark before creating the delta, so any
	// pre-existing change-log rows (for example a workspace-scope marker) are
	// excluded from the counted window.
	baseline, err := application.Changes(ctx, ItemChangesRequest{UserID: data.UserID, WorkspaceID: data.WorkspaceID})
	if err != nil {
		t.Fatalf("baseline Changes: %v", err)
	}

	const itemCount = 12
	for i := 0; i < itemCount; i++ {
		if _, err := application.Create(ctx, actor, ItemCreateInput{
			WorkspaceID: data.WorkspaceID,
			Title:       fmt.Sprintf("Delta item %d", i),
		}); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	counting := &deltaReadCountingDB{Database: base}
	result, err := newApp(counting).Changes(ctx, ItemChangesRequest{
		UserID:        data.UserID,
		WorkspaceID:   data.WorkspaceID,
		Since:         baseline.Watermark,
		SinceProvided: true,
		Limit:         100,
	})
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(result.ChangedItemIDs) != itemCount {
		t.Fatalf("changed ids = %v, want %d items", result.ChangedItemIDs, itemCount)
	}
	if counting.reads >= itemCount {
		t.Fatalf("delta resolved %d items with %d read queries; visibility must batch", itemCount, counting.reads)
	}
}
