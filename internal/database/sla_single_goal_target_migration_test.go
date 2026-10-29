//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

// TestSLASingleGoalTargetMigrationCollapsesTargets seeds the pre-migration
// sla_goal_targets shape (priority and fallback columns, multiple targets per
// goal) and asserts the migration keeps one target per goal and drops the
// columns.
func TestSLASingleGoalTargetMigrationCollapsesTargets(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	targetType := "INTEGER"
	if testutils.IsPostgres() {
		idColumn = "SERIAL PRIMARY KEY"
		targetType = "BIGINT"
	}
	if _, err := tdb.Exec(`CREATE TABLE sla_goal_targets (
		id ` + idColumn + `,
		goal_id INTEGER NOT NULL,
		position INTEGER NOT NULL DEFAULT 0,
		priority_id INTEGER,
		is_fallback BOOLEAN NOT NULL DEFAULT FALSE,
		target_ms ` + targetType + ` NOT NULL,
		calendar_id INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("seed legacy sla_goal_targets: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO sla_goal_targets (goal_id, position, priority_id, is_fallback, target_ms, calendar_id) VALUES
		(1, 0, 1, false, 100, 9),
		(1, 1, NULL, true, 200, 9),
		(2, 0, 2, false, 300, 9),
		(2, 1, 3, false, 400, 9),
		(3, 0, NULL, true, 500, 9)`); err != nil {
		t.Fatalf("seed legacy sla_goal_targets rows: %v", err)
	}

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261104_sla_single_goal_target", "")

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run sla single-target migration: %v", err)
	}

	// The fallback target survives when present; otherwise the lowest position.
	want := map[int]int64{1: 200, 2: 300, 3: 500}
	rows, err := tdb.Query(`SELECT goal_id, target_ms FROM sla_goal_targets ORDER BY goal_id`)
	if err != nil {
		t.Fatalf("read collapsed targets: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[int]int64{}
	for rows.Next() {
		var goalID int
		var targetMs int64
		if err := rows.Scan(&goalID, &targetMs); err != nil {
			t.Fatalf("scan collapsed target: %v", err)
		}
		got[goalID] = targetMs
	}
	if len(got) != len(want) {
		t.Fatalf("collapsed targets = %#v, want %#v", got, want)
	}
	for goalID, targetMs := range want {
		if got[goalID] != targetMs {
			t.Fatalf("goal %d target = %d, want %d", goalID, got[goalID], targetMs)
		}
	}

	var columns int
	if testutils.IsPostgres() {
		err = tdb.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='sla_goal_targets'
			  AND column_name IN ('priority_id', 'is_fallback')`).Scan(&columns)
	} else {
		err = tdb.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sla_goal_targets')
			WHERE name IN ('priority_id', 'is_fallback')`).Scan(&columns)
	}
	if err != nil {
		t.Fatalf("count dropped columns: %v", err)
	}
	if columns != 0 {
		t.Fatalf("sla_goal_targets still has %d priority/fallback columns", columns)
	}

	// The migration check makes a repeat initialize a no-op.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
}
