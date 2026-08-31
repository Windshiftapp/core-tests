package repository

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
)

type onCallReadCountingDB struct {
	database.Database
	reads int
}

func (db *onCallReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func (db *onCallReadCountingDB) QueryRow(query string, args ...interface{}) *sql.Row {
	db.reads++
	return db.Database.QueryRow(query, args...)
}

func TestListSchedulesForTeamHydratesOverviewWithBoundedReads(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "oncall-overview.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}
	userOneID := insertID(
		"first user",
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('one@example.test', 'one', 'User', 'One')`,
	)
	userTwoID := insertID(
		"second user",
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('two@example.test', 'two', 'User', 'Two')`,
	)
	teamID := insertID(
		"team",
		`INSERT INTO teams (name, description, created_by) VALUES ('Overview Team', 'Team description', ?)`,
		userOneID,
	)
	scheduleOneID := insertID(
		"first schedule",
		`INSERT INTO on_call_schedules (team_id, name, description, timezone, created_by) VALUES (?, 'Alpha', 'First', 'UTC', ?)`,
		teamID,
		userOneID,
	)
	scheduleTwoID := insertID(
		"second schedule",
		`INSERT INTO on_call_schedules (team_id, name, description, timezone, created_by) VALUES (?, 'Beta', 'Second', 'UTC', ?)`,
		teamID,
		userOneID,
	)
	layerOneID := insertID(
		"first layer",
		`INSERT INTO on_call_schedule_layers (schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date) VALUES (?, 'Primary', 1, 'daily', 1, '00:00', '2000-01-01')`,
		scheduleOneID,
	)
	layerTwoID := insertID(
		"second layer",
		`INSERT INTO on_call_schedule_layers (schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date) VALUES (?, 'Secondary', 2, 'weekly', 7, '00:00', '2000-01-01')`,
		scheduleOneID,
	)
	layerThreeID := insertID(
		"third layer",
		`INSERT INTO on_call_schedule_layers (schedule_id, name, priority, rotation_type, rotation_interval_days, handoff_time, start_date) VALUES (?, 'Only', 1, 'daily', 1, '00:00', '2000-01-01')`,
		scheduleTwoID,
	)
	for _, assignment := range []struct {
		layerID int
		userID  int
	}{
		{layerID: layerOneID, userID: userOneID},
		{layerID: layerTwoID, userID: userTwoID},
		{layerID: layerThreeID, userID: userTwoID},
	} {
		if _, err := db.ExecWrite(
			`INSERT INTO on_call_schedule_layer_members (layer_id, user_id, position) VALUES (?, ?, 1)`,
			assignment.layerID,
			assignment.userID,
		); err != nil {
			t.Fatalf("insert layer member: %v", err)
		}
	}
	if _, err := db.ExecWrite(`
		INSERT INTO on_call_schedule_overrides
			(schedule_id, user_id, override_user_id, start_time, end_time, reason, created_by)
		VALUES (?, ?, ?, ?, ?, 'Coverage', ?)
	`, scheduleOneID, userOneID, userTwoID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), userOneID); err != nil {
		t.Fatalf("insert override: %v", err)
	}

	countingDB := &onCallReadCountingDB{Database: db}
	schedules, err := NewOnCallRepository(countingDB).ListSchedulesForTeam(teamID, true)
	if err != nil {
		t.Fatalf("ListSchedulesForTeam: %v", err)
	}
	if countingDB.reads != 4 {
		t.Fatalf("read queries = %d, want 4 independent of schedule/layer count", countingDB.reads)
	}
	if len(schedules) != 2 {
		t.Fatalf("schedules = %+v, want two", schedules)
	}
	if schedules[0].ID != scheduleOneID || len(schedules[0].Layers) != 2 {
		t.Fatalf("first schedule = %+v, want schedule %d with two layers", schedules[0], scheduleOneID)
	}
	if len(schedules[0].Layers[0].Members) != 1 || schedules[0].Layers[0].Members[0].UserID != userOneID {
		t.Fatalf("first layer members = %+v, want user %d", schedules[0].Layers[0].Members, userOneID)
	}
	if len(schedules[0].Overrides) != 1 || schedules[0].Overrides[0].OverrideUserID != userTwoID {
		t.Fatalf("first schedule overrides = %+v, want active override user %d", schedules[0].Overrides, userTwoID)
	}
	if schedules[1].ID != scheduleTwoID || len(schedules[1].Layers) != 1 || schedules[1].Layers[0].ID != layerThreeID {
		t.Fatalf("second schedule = %+v, want schedule %d with layer %d", schedules[1], scheduleTwoID, layerThreeID)
	}
}
