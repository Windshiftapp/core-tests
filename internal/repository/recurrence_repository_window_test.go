//go:build test

package repository

import (
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// GetExistingInstanceDates must bound its lookup to the requested generation
// window instead of loading the rule's entire instance history.
func TestGetExistingInstanceDatesReturnsOnlyWindowDates(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	insertTemplate := func(t *testing.T, number int, title string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, FALSE, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			RETURNING id
		`, data.WorkspaceID, number, title, "rt"+string(rune('0'+number))).Scan(&id); err != nil {
			t.Fatalf("insert template item %q: %v", title, err)
		}
		return id
	}

	creatorID := data.UserID
	insertTemplate(t, 1, "Window template")
	otherTemplateID := insertTemplate(t, 2, "Other rule template")

	recurrenceRepo := NewRecurrenceRepository(db)
	start := time.Now().UTC().Truncate(24 * time.Hour)
	newRule := func(t *testing.T, templateID int) int {
		t.Helper()
		ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
			TemplateItemID: templateID,
			WorkspaceID:    data.WorkspaceID,
			RRule:          "FREQ=DAILY;COUNT=30",
			DtStart:        start,
			Timezone:       "UTC",
			LeadTimeDays:   2,
			IsActive:       true,
			CreatedBy:      &creatorID,
		})
		if err != nil {
			t.Fatalf("create recurrence rule: %v", err)
		}
		return ruleID
	}
	ruleID := newRule(t, 1)
	otherRuleID := newRule(t, otherTemplateID)

	insertInstance := func(t *testing.T, ruleID int, day time.Time) {
		t.Helper()
		var itemID int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
			VALUES (?, (SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?), 'Generated instance', FALSE, 'gi' || (SELECT COALESCE(MAX(workspace_item_number), 0) + 1 FROM items WHERE workspace_id = ?), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			RETURNING id
		`, data.WorkspaceID, data.WorkspaceID, data.WorkspaceID).Scan(&itemID); err != nil {
			t.Fatalf("insert instance item: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO recurrence_instances (recurrence_rule_id, instance_item_id, scheduled_date, sequence_number, created_at)
			VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		`, ruleID, itemID, day); err != nil {
			t.Fatalf("insert instance at %s: %v", day.Format(time.RFC3339), err)
		}
	}

	day := func(offset int) time.Time {
		return start.AddDate(0, 0, offset)
	}

	// History outside the window (before and after) plus in-window dates
	// including both boundary days, and another rule's date inside the window.
	insertInstance(t, ruleID, day(0))
	insertInstance(t, ruleID, day(2))
	insertInstance(t, ruleID, day(5))
	insertInstance(t, ruleID, day(9))
	insertInstance(t, ruleID, day(10))
	insertInstance(t, otherRuleID, day(5))

	dates, err := recurrenceRepo.GetExistingInstanceDates(ruleID, day(2), day(9))
	if err != nil {
		t.Fatalf("get existing instance dates: %v", err)
	}

	want := map[string]bool{
		day(2).Format("2006-01-02"): true,
		day(5).Format("2006-01-02"): true,
		day(9).Format("2006-01-02"): true,
	}
	if len(dates) != len(want) {
		t.Fatalf("existing dates = %v, want exactly %v", dates, want)
	}
	for key := range want {
		if !dates[key] {
			t.Fatalf("existing dates = %v, missing in-window date %s", dates, key)
		}
	}
	for _, key := range []string{day(0).Format("2006-01-02"), day(10).Format("2006-01-02")} {
		if dates[key] {
			t.Fatalf("existing dates = %v, out-of-window date %s must not be loaded", dates, key)
		}
	}
}
