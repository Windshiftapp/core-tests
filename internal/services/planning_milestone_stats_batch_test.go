package services

import (
	"database/sql"
	"testing"

	"windshift/internal/database"
)

type milestoneStatisticsReadCountingDB struct {
	database.Database
	reads int
}

func (db *milestoneStatisticsReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestMilestoneTestStatisticsBatchUsesOneScopedRead(t *testing.T) {
	fixture := seedPlanningScopeFixture(t)
	secondGlobal := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Second shared milestone', '', 'planning', true, NULL)
	`)
	visibleLocal := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Visible local milestone', '', 'planning', false, ?)
	`, fixture.workspaceA)
	hiddenLocal := planningScopeInsertID(t, fixture.db, `
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Hidden local milestone', '', 'planning', false, ?)
	`, fixture.workspaceB)
	for _, testSet := range []struct {
		name        string
		workspaceID int
		milestoneID int
	}{
		{"Second shared tests", fixture.workspaceA, secondGlobal},
		{"Visible local tests", fixture.workspaceA, visibleLocal},
		{"Hidden local tests", fixture.workspaceB, hiddenLocal},
	} {
		planningScopeInsertID(t, fixture.db, `
			INSERT INTO test_sets (workspace_id, name, description, milestone_id)
			VALUES (?, ?, '', ?)
		`, testSet.workspaceID, testSet.name, testSet.milestoneID)
	}

	countingDB := &milestoneStatisticsReadCountingDB{Database: fixture.db}
	statistics, err := NewPlanningService(countingDB).GetMilestoneTestStatisticsBatch(
		[]int{fixture.milestoneID, secondGlobal, visibleLocal, hiddenLocal, secondGlobal},
		[]int{fixture.workspaceA},
	)
	if err != nil {
		t.Fatalf("GetMilestoneTestStatisticsBatch: %v", err)
	}
	if countingDB.reads != 1 {
		t.Fatalf("read queries = %d, want 1 independent of milestone count", countingDB.reads)
	}
	if len(statistics) != 3 {
		t.Fatalf("statistics = %+v, want two global and one visible local milestone", statistics)
	}
	for _, milestoneID := range []int{fixture.milestoneID, secondGlobal, visibleLocal} {
		stats := statistics[milestoneID]
		if stats == nil || stats.TotalTestPlans != 1 {
			t.Fatalf("milestone %d stats = %+v, want one visible test plan", milestoneID, stats)
		}
	}
	if _, exists := statistics[hiddenLocal]; exists {
		t.Fatalf("hidden local milestone %d leaked through batch", hiddenLocal)
	}
}
