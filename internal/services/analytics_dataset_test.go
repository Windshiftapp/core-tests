//go:build test

package services_test

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestAnalyticsQLResolutionUsesItemListJoins(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	f := factory.NewTestFactory(tdb.GetDatabase())

	lookup := func(query string, args ...any) (int, string) {
		t.Helper()
		var id int
		var name string
		if err := tdb.QueryRow(query, args...).Scan(&id, &name); err != nil {
			t.Fatalf("lookup fixture value: %v", err)
		}
		return id, name
	}

	openID, openName := lookup(`SELECT id, name FROM statuses WHERE name = 'Open'`)
	inProgressID, _ := lookup(`SELECT id, name FROM statuses WHERE name = 'In Progress'`)
	doneID, _ := lookup(`SELECT id, name FROM statuses WHERE name = 'Done'`)
	mediumID, _ := lookup(`SELECT id, name FROM priorities WHERE name = 'Medium'`)
	highID, highName := lookup(`SELECT id, name FROM priorities WHERE name = 'High'`)
	lowID, _ := lookup(`SELECT id, name FROM priorities WHERE name = 'Low'`)
	taskID, _ := lookup(`SELECT id, name FROM item_types WHERE name = 'Task'`)
	bugID, bugName := lookup(`SELECT id, name FROM item_types WHERE name = 'Bug'`)

	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	createItem := func(title string, statusID, priorityID, itemTypeID int) int {
		t.Helper()
		id, err := f.CreateItem(factory.CreateItemOpts{
			WorkspaceID: data.WorkspaceID,
			Title:       title,
			StatusID:    &statusID,
			PriorityID:  &priorityID,
			ItemTypeID:  &itemTypeID,
			CreatorID:   &data.UserID,
			CreatedAt:   &createdAt,
			UpdatedAt:   &createdAt,
		})
		if err != nil {
			t.Fatalf("create fixture item: %v", err)
		}
		return id
	}

	createItem("Open task", openID, mediumID, taskID)
	createItem("In-progress bug", inProgressID, highID, bugID)
	completedID := createItem("Completed task", doneID, lowID, taskID)
	completedAt := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	if _, err := tdb.Exec(`
		INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
		VALUES (?, ?, ?, 'status_id', ?, ?)
	`, completedID, data.UserID, completedAt, openID, doneID); err != nil {
		t.Fatalf("insert completion history: %v", err)
	}

	service := services.NewAnalyticsService(tdb.GetDatabase())
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{name: "status", query: fmt.Sprintf(`status = %q`, openName), want: 1},
		{name: "priority", query: fmt.Sprintf(`priority = %q`, highName), want: 1},
		{name: "item type", query: fmt.Sprintf(`type = %q`, bugName), want: 1},
		{name: "completed at", query: `completed_at IS NOT NULL`, want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := service.GetAnalytics(services.ResolveDatasetParams{
				WorkspaceID: data.WorkspaceID,
				QLQuery:     tc.query,
				UserID:      data.UserID,
				StartDate:   createdAt,
				EndDate:     completedAt,
			})
			if err != nil {
				t.Fatalf("GetAnalytics(%q): %v", tc.query, err)
			}
			if result.Dataset.TotalItems != tc.want {
				t.Fatalf("GetAnalytics(%q) dataset total = %d, want %d", tc.query, result.Dataset.TotalItems, tc.want)
			}
		})
	}

}
