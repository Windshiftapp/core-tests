package services

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestMilestoneProgressCountsDoneItemsAsCompleted(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "milestone-progress.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	workspaceID := mustExecInsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Test', 'TST')`)
	doneID := mustQueryID(t, db, `SELECT id FROM statuses WHERE name = 'Done'`)
	milestoneID := mustExecInsertID(t, db, `INSERT INTO milestones (name, is_global, workspace_id) VALUES ('1.0.0', false, ?)`, workspaceID)
	if _, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:  workspaceID,
		Title:        "Completed feature",
		StatusID:     &doneID,
		MilestoneIDs: []int{milestoneID},
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	progress, err := NewPlanningService(db).GetMilestoneProgress(milestoneID, []int{workspaceID})
	if err != nil {
		t.Fatalf("GetMilestoneProgress: %v", err)
	}
	if progress.TotalItems != 1 || progress.CompletedItems != 1 || progress.PercentComplete != 100 {
		t.Fatalf("progress = %d/%d (%.0f%%), want 1/1 (100%%)", progress.CompletedItems, progress.TotalItems, progress.PercentComplete)
	}
	if len(progress.StatusBreakdown) != 1 || !progress.StatusBreakdown[0].IsCompleted {
		t.Fatalf("status breakdown = %#v, want one completed category", progress.StatusBreakdown)
	}
}
