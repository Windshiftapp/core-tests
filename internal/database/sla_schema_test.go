//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

func TestSLASchemaCreatesTables(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	tables := []string{
		"team_workspace_bindings",
		"working_calendars",
		"sla_workspace_state",
		"sla_metrics",
		"sla_metric_conditions",
		"sla_goals",
		"sla_goal_targets",
		"item_sla_cycles",
		"item_sla_cycle_thresholds",
		"sla_jobs",
	}
	for _, table := range tables {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("table %s is missing or unreadable: %v", table, err)
		}
	}
}

func TestSLAJobsCheckRequiresExactlyOneSubject(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	// No subject at all.
	if _, err := db.Exec(`INSERT INTO sla_jobs (kind, due_at) VALUES ('breach', CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("expected the sla_jobs CHECK to reject a job with no subject")
	}
}

func TestWorkingCalendarsCheckRequiresExactlyOneOwner(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	if _, err := db.Exec(`INSERT INTO working_calendars (name, timezone, weekly_intervals) VALUES ('x', 'UTC', '{}')`); err == nil {
		t.Fatal("expected the working_calendars CHECK to reject a calendar with no owner")
	}
	if _, err := db.Exec(`INSERT INTO working_calendars (workspace_id, team_id, name, timezone, weekly_intervals) VALUES (1, 1, 'x', 'UTC', '{}')`); err == nil {
		t.Fatal("expected the working_calendars CHECK to reject a calendar with two owners")
	}
}

func TestSLASchemaCreatesIndexes(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	// Query the engine-specific catalog rather than assuming pg_indexes.
	wantIndexes := []string{
		"uq_sla_jobs_subject",
		"idx_sla_jobs_due",
		"idx_sla_jobs_cycle",
		"idx_sla_jobs_item",
		"idx_sla_jobs_metric",
		"uq_item_sla_cycles_ongoing",
		"idx_item_sla_cycles_item_ongoing",
		"idx_item_sla_cycles_metric_deadline",
		"idx_item_sla_cycles_metric",
		"idx_item_sla_cycles_goal",
		"idx_item_sla_cycles_calendar",
	}
	for _, name := range wantIndexes {
		var count int
		var err error
		if testutils.IsPostgres() {
			err = db.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, name).Scan(&count)
		} else {
			err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count)
		}
		if err != nil {
			t.Fatalf("query index %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("index %s missing (count=%d)", name, count)
		}
	}
}
