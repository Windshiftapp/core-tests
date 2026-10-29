//go:build test

package services

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestCreateItemCommitsCreationHistoryAtTheSourceOccurrenceTime(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	createdAt := time.Date(2020, time.March, 4, 5, 6, 7, 0, time.UTC)

	itemID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "Imported chronology", Description: "Imported description",
		StatusID: &data.StatusID, PriorityID: &data.PriorityID, CreatorID: &data.UserID,
		CreatedAt: &createdAt, UpdatedAt: &createdAt, EventMetadata: itemevents.Import("jira-job-7"),
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}

	rows, err := tdb.Query(`
		SELECT field_name, old_value, new_value, changed_at
		FROM item_history
		WHERE item_id = ?
		ORDER BY id
	`, itemID)
	if err != nil {
		t.Fatalf("query creation history: %v", err)
	}
	defer rows.Close()
	creationRows := 0
	foundTitle := false
	for rows.Next() {
		var field, oldValue, newValue string
		var changedAt time.Time
		if err := rows.Scan(&field, &oldValue, &newValue, &changedAt); err != nil {
			t.Fatalf("scan creation history: %v", err)
		}
		creationRows++
		if oldValue != "" {
			t.Fatalf("creation history old value = %q, want empty", oldValue)
		}
		if !changedAt.Equal(createdAt) {
			t.Fatalf("creation history changed_at = %s, want %s", changedAt, createdAt)
		}
		if field == "title" {
			foundTitle = newValue == "Imported chronology"
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate creation history: %v", err)
	}
	if creationRows == 0 || !foundTitle {
		t.Fatalf("creation history rows = %d, title recorded = %t", creationRows, foundTitle)
	}

	if _, err := NewItemUpdateService(tdb.GetDatabase()).UpdateItem(UpdateItemRequest{
		ItemID: int(itemID), UserID: data.UserID,
		UpdateData: map[string]any{"title": "Updated chronology"},
	}); err != nil {
		t.Fatalf("UpdateItem() error = %v", err)
	}
	history, err := repository.NewItemRepository(tdb.GetDatabase()).GetHistory(int(itemID), 0)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	var titleRows []repository.HistoryEntry
	for _, entry := range history {
		if entry.FieldName == "title" {
			titleRows = append(titleRows, entry)
		}
	}
	if len(titleRows) != 2 {
		t.Fatalf("title history = %+v, want update and creation", titleRows)
	}
	if titleRows[0].OldValue != "Imported chronology" || titleRows[0].NewValue != "Updated chronology" ||
		titleRows[1].OldValue != "" || titleRows[1].NewValue != "Imported chronology" {
		t.Fatalf("ordered title history = %+v", titleRows)
	}
}

func TestCreateItemRollsBackItemEventAndHistoryWhenCreationHistoryFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	_, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "Atomic history rollback",
		StatusID: &data.StatusID, PriorityID: &data.PriorityID, CreatorID: &data.UserID,
		AfterCreate: func(_ context.Context, tx database.Tx, _ int) error {
			// The trigger is a transaction-local failure injector. It uses the
			// production schema and rolls back with the creation transaction.
			return installCreationHistoryFailure(tdb.GetDriverName(), tx)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "record item creation history") {
		t.Fatalf("CreateItem() error = %v, want creation-history failure", err)
	}

	counts := []struct {
		name  string
		query string
	}{
		{name: "item", query: "SELECT COUNT(*) FROM items WHERE title = 'Atomic history rollback'"},
		{name: "event", query: "SELECT COUNT(*) FROM domain_events WHERE event_type = 'item.created'"},
		{name: "history", query: "SELECT COUNT(*) FROM item_history"},
	}
	for _, check := range counts {
		var count int
		if err := tdb.QueryRow(check.query).Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s row count = %d, want 0 after rollback", check.name, count)
		}
	}
}

func TestCopyItemRollsBackWhenCreationHistoryFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	sourceID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "Copy history source",
		StatusID: &data.StatusID, PriorityID: &data.PriorityID, CreatorID: &data.UserID,
	})
	if err != nil {
		t.Fatalf("create source item: %v", err)
	}
	if err := installCreationHistoryFailure(tdb.GetDriverName(), tdb.GetDatabase()); err != nil {
		t.Fatalf("install history failure: %v", err)
	}

	_, err = NewItemCRUDService(tdb.GetDatabase()).Copy(int(sourceID), CopyOptions{
		NewTitle: "Copy history rollback", CreatorID: data.UserID,
	})
	// Copy now runs through the shared creation path, so the rollback surfaces
	// the canonical creation-history failure.
	if err == nil || !strings.Contains(err.Error(), "record item creation history") {
		t.Fatalf("Copy() error = %v, want creation-history failure", err)
	}
	var copiedItems int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM items WHERE title = 'Copy history rollback'").Scan(&copiedItems); err != nil {
		t.Fatalf("count copied items: %v", err)
	}
	if copiedItems != 0 {
		t.Fatalf("copied item count = %d, want 0", copiedItems)
	}
	var createdEvents int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = 'item.created'").Scan(&createdEvents); err != nil {
		t.Fatalf("count item.created events: %v", err)
	}
	if createdEvents != 1 {
		t.Fatalf("item.created event count = %d, want source event only", createdEvents)
	}
}

type historyFailureExecutor interface {
	Exec(string, ...any) (sql.Result, error)
}

func installCreationHistoryFailure(driver string, executor historyFailureExecutor) error {
	if driver == "postgres" {
		if _, err := executor.Exec(`
			CREATE FUNCTION wi1119_fail_history() RETURNS trigger
			LANGUAGE plpgsql AS $function$
			BEGIN
				RAISE EXCEPTION 'forced history failure';
			END;
			$function$
		`); err != nil {
			return err
		}
		_, err := executor.Exec(`
			CREATE TRIGGER wi1119_fail_history
			BEFORE INSERT ON item_history
			FOR EACH ROW EXECUTE FUNCTION wi1119_fail_history()
		`)
		return err
	}
	_, err := executor.Exec(`
		CREATE TRIGGER wi1119_fail_history
		BEFORE INSERT ON item_history
		BEGIN
			SELECT RAISE(ABORT, 'forced history failure');
		END
	`)
	return err
}
