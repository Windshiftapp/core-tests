package services

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
)

type burndownHistoryFixture struct {
	db          database.Database
	service     *PlanningService
	workspaceID int
	iterationID int
	openStatus  int
	doneStatus  int
	userID      int
}

func newBurndownHistoryFixture(t *testing.T) *burndownHistoryFixture {
	t.Helper()
	db := newPlanningScopeTestDB(t)
	workspaceID := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Burndown history', 'BDH', '', true, false)
	`)
	userID := planningScopeInsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('burndown@example.test', 'burndown', 'Burn', 'Down', '', true)
	`)
	openCategory := planningScopeInsertID(t, db, `
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Burndown open', '#123456', '', false)
	`)
	doneCategory := planningScopeInsertID(t, db, `
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Burndown done', '#654321', '', true)
	`)
	openStatus := planningScopeInsertID(t, db, `
		INSERT INTO statuses (name, description, category_id)
		VALUES ('Burndown Open', '', ?)
	`, openCategory)
	doneStatus := planningScopeInsertID(t, db, `
		INSERT INTO statuses (name, description, category_id)
		VALUES ('Burndown Done', '', ?)
	`, doneCategory)
	iterationID := planningScopeInsertID(t, db, `
		INSERT INTO iterations (
			name, description, start_date, end_date, status, is_global, workspace_id
		) VALUES ('Historical iteration', '', '2026-07-01', '2026-07-05', 'completed', false, ?)
	`, workspaceID)
	return &burndownHistoryFixture{
		db:          db,
		service:     NewPlanningService(db),
		workspaceID: workspaceID,
		iterationID: iterationID,
		openStatus:  openStatus,
		doneStatus:  doneStatus,
		userID:      userID,
	}
}

func (f *burndownHistoryFixture) addItem(
	t *testing.T,
	title, createdAt string,
	currentIteration interface{},
	currentStatus int,
) int {
	t.Helper()
	created, err := time.Parse("2006-01-02 15:04:05-07:00", createdAt)
	if err != nil {
		t.Fatalf("parse createdAt %q: %v", createdAt, err)
	}
	var iterationID *int
	if id, ok := currentIteration.(int); ok && id != 0 {
		iterationID = &id
	}
	itemID64, err := CreateItem(f.db, ItemCreationParams{
		WorkspaceID: f.workspaceID,
		Title:       title,
		StatusID:    &currentStatus,
		IterationID: iterationID,
		CreatedAt:   &created,
		UpdatedAt:   &created,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(itemID64)
}

func (f *burndownHistoryFixture) history(
	t *testing.T,
	itemID int,
	changedAt, field, oldValue, newValue string,
) {
	t.Helper()
	if _, err := f.db.ExecWrite(`
		INSERT INTO item_history (
			item_id, user_id, changed_at, field_name, old_value, new_value
		) VALUES (?, ?, ?, ?, ?, ?)
	`, itemID, f.userID, changedAt, field, oldValue, newValue); err != nil {
		t.Fatalf("insert %s history: %v", field, err)
	}
}

func burndownPointsByDate(points []BurndownDataPoint) map[string]BurndownDataPoint {
	result := make(map[string]BurndownDataPoint, len(points))
	for _, point := range points {
		result[point.Date] = point
	}
	return result
}

func TestIterationBurndownReconstructsMembershipAndStatusHistory(t *testing.T) {
	f := newBurndownHistoryFixture(t)
	iterationValue := fmt.Sprintf("%d", f.iterationID)

	carried := f.addItem(t, "Carried after completion", "2026-06-30 09:00:00+00:00", nil, f.openStatus)
	f.history(t, carried, "2026-06-30 09:00:00+00:00", "iteration_id", "", iterationValue)
	f.history(t, carried, "2026-07-06 09:00:00+00:00", "iteration_id", iterationValue, "")

	addedThenRemoved := f.addItem(t, "Scope change", "2026-07-03 08:00:00+00:00", nil, f.openStatus)
	f.history(t, addedThenRemoved, "2026-07-03 10:00:00+00:00", "iteration_id", "", iterationValue)
	f.history(t, addedThenRemoved, "2026-07-04 10:00:00+00:00", "iteration_id", iterationValue, "")

	completed := f.addItem(t, "Completed work", "2026-06-30 09:00:00+00:00", f.iterationID, f.doneStatus)
	f.history(t, completed, "2026-06-30 09:00:00+00:00", "iteration_id", "", iterationValue)
	f.history(
		t, completed, "2026-07-03 12:00:00+00:00", "status_id",
		fmt.Sprintf("%d", f.openStatus), fmt.Sprintf("%d", f.doneStatus),
	)

	result, err := f.service.GetIterationBurndown(f.iterationID, []int{f.workspaceID})
	if err != nil {
		t.Fatalf("GetIterationBurndown: %v", err)
	}
	if result.TotalItems != 3 {
		t.Fatalf("TotalItems = %d, want 3 distinct historical members", result.TotalItems)
	}
	points := burndownPointsByDate(result.DataPoints)
	expected := map[string]BurndownDataPoint{
		"2026-07-01": {Remaining: 2, Completed: 0, Ideal: 2},
		"2026-07-02": {Remaining: 2, Completed: 0, Ideal: 2},
		"2026-07-03": {Remaining: 2, Completed: 1, Ideal: 1},
		"2026-07-04": {Remaining: 1, Completed: 1, Ideal: 1},
		"2026-07-05": {Remaining: 1, Completed: 1, Ideal: 0},
	}
	for date, want := range expected {
		got, ok := points[date]
		if !ok {
			t.Fatalf("missing point for %s: %+v", date, result.DataPoints)
		}
		if got.Remaining != want.Remaining ||
			got.Completed != want.Completed ||
			got.Ideal != want.Ideal {
			t.Fatalf("point %s = %+v, want remaining/completed/ideal %d/%d/%d",
				date, got, want.Remaining, want.Completed, want.Ideal)
		}
	}
}

func TestIterationBurndownRetainsHistoryWhenNoItemsRemainAssigned(t *testing.T) {
	f := newBurndownHistoryFixture(t)
	iterationValue := fmt.Sprintf("%d", f.iterationID)
	itemID := f.addItem(t, "No longer assigned", "2026-06-30 09:00:00+00:00", nil, f.openStatus)
	f.history(t, itemID, "2026-06-30 09:00:00+00:00", "iteration_id", "", iterationValue)
	f.history(t, itemID, "2026-07-06 09:00:00+00:00", "iteration_id", iterationValue, "")

	result, err := f.service.GetIterationBurndown(f.iterationID, []int{f.workspaceID})
	if err != nil {
		t.Fatalf("GetIterationBurndown: %v", err)
	}
	if result.TotalItems != 1 || len(result.DataPoints) != 5 {
		t.Fatalf("historical burndown = %+v, want one item across five days", result)
	}
	for _, point := range result.DataPoints {
		if point.Remaining != 1 || point.Completed != 0 {
			t.Fatalf("point %s = %+v, want one remaining historical item", point.Date, point)
		}
	}
}
