//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestFindAllWithDetails_CompletedSince covers the completed_since filter that
// caps the indefinitely-growing "done" list on personal views: items in a
// completed status are pruned to those that entered that status on/after the
// cutoff, while non-completed items always pass.
func TestFindAllWithDetails_CompletedSince(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	repo := NewItemRepository(tdb.GetDatabase())
	now := time.Now()

	must := func(err error, msg string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}

	// Workspace
	var wsID64 int64
	must(tdb.DB.QueryRow(`
		INSERT INTO workspaces (name, key, description, created_at, updated_at)
		VALUES ('Completed Since', 'CSN', '', ?, ?) RETURNING id
	`, now, now).Scan(&wsID64), "create workspace")
	wsID := int(wsID64)

	// Status categories: one open (is_completed = false), one done (is_completed = true).
	var openCatID, doneCatID int64
	must(tdb.DB.QueryRow(`
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Open CSN', '#3b82f6', false) RETURNING id
	`).Scan(&openCatID), "create open category")
	must(tdb.DB.QueryRow(`
		INSERT INTO status_categories (name, color, is_completed) VALUES ('Done CSN', '#22c55e', true) RETURNING id
	`).Scan(&doneCatID), "create done category")

	// Statuses
	var openStatusID, doneStatusID int64
	must(tdb.DB.QueryRow(`
		INSERT INTO statuses (name, category_id) VALUES ('Open CSN', ?) RETURNING id
	`, openCatID).Scan(&openStatusID), "create open status")
	must(tdb.DB.QueryRow(`
		INSERT INTO statuses (name, category_id) VALUES ('Done CSN', ?) RETURNING id
	`, doneCatID).Scan(&doneStatusID), "create done status")

	// User (reuse any existing, else create one)
	var userID int
	if err := tdb.DB.QueryRow("SELECT id FROM users LIMIT 1").Scan(&userID); err != nil {
		var userID64 int64
		must(tdb.DB.QueryRow(`
			INSERT INTO users (username, email, first_name, last_name, password_hash, created_at, updated_at)
			VALUES ('csnuser', 'csn@example.com', 'CSN', 'User', 'hash', ?, ?) RETURNING id
		`, now, now).Scan(&userID64), "create user")
		userID = int(userID64)
	}

	// Helper to create an item; frac_index has a global UNIQUE index.
	createItem := func(num int, title, frac string, statusID int64) int {
		t.Helper()
		var id64 int64
		must(tdb.DB.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, is_task,
			                   frac_index, path, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, false, ?, ?, ?, ?) RETURNING id
		`, wsID, num, title, statusID, frac, "/"+frac+"/", now, now).Scan(&id64), "create item "+title)
		return int(id64)
	}

	// Records the transition into the item's current (done) status at the given time.
	recordDoneAt := func(itemID int, at time.Time) {
		t.Helper()
		_, err := tdb.Exec(`
			INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
			VALUES (?, ?, ?, 'status_id', ?, ?)
		`, itemID, userID, at, testutils.IntToString(int(openStatusID)), testutils.IntToString(int(doneStatusID)))
		must(err, "record history")
	}

	openItem := createItem(1, "Open item", "csn0", openStatusID)
	recentDone := createItem(2, "Recently done", "csn1", doneStatusID)
	oldDone := createItem(3, "Long ago done", "csn2", doneStatusID)

	recordDoneAt(recentDone, now.AddDate(0, 0, -2))
	recordDoneAt(oldDone, now.AddDate(0, 0, -30))

	listIDs := func(cutoff *string) map[int]bool {
		t.Helper()
		items, _, err := repo.FindAllWithDetails(ItemListParams{
			WorkspaceIDs: []int{wsID},
			Filters:      ItemFilters{CompletedSince: cutoff},
			Pagination:   PaginationParams{Limit: 100},
		})
		if err != nil {
			t.Fatalf("FindAllWithDetails: %v", err)
		}
		ids := make(map[int]bool, len(items))
		for _, it := range items {
			ids[it.ID] = true
		}
		return ids
	}

	t.Run("no cutoff returns all items", func(t *testing.T) {
		ids := listIDs(nil)
		for _, want := range []int{openItem, recentDone, oldDone} {
			if !ids[want] {
				t.Errorf("expected item %d in unfiltered results", want)
			}
		}
	})

	t.Run("7-day cutoff keeps open + recent done, drops old done", func(t *testing.T) {
		cutoff := now.AddDate(0, 0, -7).Format("2006-01-02")
		ids := listIDs(&cutoff)
		if !ids[openItem] {
			t.Error("open item must always pass the completed_since filter")
		}
		if !ids[recentDone] {
			t.Error("recently-done item should be within the 7-day window")
		}
		if ids[oldDone] {
			t.Error("long-ago-done item should be excluded by the 7-day window")
		}
	})
}
