package services

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

type boardStatusReadCountingDB struct {
	database.Database
	reads int
}

func (db *boardStatusReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestWorkspaceStatusUnionUsesOneReadForMultipleWorkspaces(t *testing.T) {
	db := newBoardConfigurationDataDB(t, "board-status-union.db")
	insertID := boardConfigurationInsertID(t, db)

	categoryID := insertID("category", `INSERT INTO status_categories (name, color) VALUES ('Board union', '#123456')`)
	statusA := insertID("status A", `INSERT INTO statuses (name, category_id) VALUES ('Board A', ?)`, categoryID)
	statusB := insertID("status B", `INSERT INTO statuses (name, category_id) VALUES ('Board B', ?)`, categoryID)
	statusC := insertID("status C", `INSERT INTO statuses (name, category_id) VALUES ('Board C', ?)`, categoryID)
	statusD := insertID("status D", `INSERT INTO statuses (name, category_id) VALUES ('Board D', ?)`, categoryID)
	workflowOne := insertID("workflow one", `INSERT INTO workflows (name) VALUES ('Board workflow one')`)
	workflowTwo := insertID("workflow two", `INSERT INTO workflows (name) VALUES ('Board workflow two')`)
	if _, err := db.ExecWrite(
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, ?, ?), (?, ?, ?)`,
		workflowOne, statusA, statusB, workflowTwo, statusC, statusD,
	); err != nil {
		t.Fatalf("insert transitions: %v", err)
	}
	configOne := insertID("config one", `INSERT INTO configuration_sets (name, workflow_id) VALUES ('Board config one', ?)`, workflowOne)
	configTwo := insertID("config two", `INSERT INTO configuration_sets (name, workflow_id) VALUES ('Board config two', ?)`, workflowTwo)
	workspaceOne := insertID("workspace one", `INSERT INTO workspaces (name, key) VALUES ('Board workspace one', 'BW1')`)
	workspaceTwo := insertID("workspace two", `INSERT INTO workspaces (name, key) VALUES ('Board workspace two', 'BW2')`)
	if _, err := db.ExecWrite(
		`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?), (?, ?)`,
		workspaceOne, configOne, workspaceTwo, configTwo,
	); err != nil {
		t.Fatalf("assign configuration sets: %v", err)
	}

	countingDB := &boardStatusReadCountingDB{Database: db}
	statuses, err := NewWorkspaceService(countingDB).GetStatusesForWorkspaces([]int{workspaceOne, workspaceTwo})
	if err != nil {
		t.Fatalf("GetStatusesForWorkspaces: %v", err)
	}
	if countingDB.reads != 1 {
		t.Fatalf("status reads = %d, want 1 independent of workspace count", countingDB.reads)
	}
	want := map[int]bool{statusA: true, statusB: true, statusC: true, statusD: true}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %+v, want exactly four workflow statuses", statuses)
	}
	for _, status := range statuses {
		if !want[status.ID] {
			t.Fatalf("unexpected status %+v", status)
		}
	}
}

func TestItemCQLWorkspaceProjectionDoesNotLoadMatchingItems(t *testing.T) {
	db := newBoardConfigurationDataDB(t, "board-workspace-projection.db")
	insertID := boardConfigurationInsertID(t, db)
	workspaceOne := insertID("workspace one", `INSERT INTO workspaces (name, key) VALUES ('Projection one', 'BP1')`)
	workspaceTwo := insertID("workspace two", `INSERT INTO workspaces (name, key) VALUES ('Projection two', 'BP2')`)
	workspaceDenied := insertID("denied workspace", `INSERT INTO workspaces (name, key) VALUES ('Projection denied', 'BPD')`)
	itemWorkspace := map[string]int{
		"Board match one": workspaceOne,
		"Unrelated":       workspaceOne,
		"Board match two": workspaceTwo,
		"Board denied":    workspaceDenied,
	}
	for _, title := range []string{"Board match one", "Unrelated", "Board match two", "Board denied"} {
		if _, err := CreateItem(db, ItemCreationParams{WorkspaceID: itemWorkspace[title], Title: title}); err != nil {
			t.Fatalf("insert item %q: %v", title, err)
		}
	}

	workspaceIDs, err := NewItemCRUDService(db).ListDistinctWorkspaceIDsWithQLContext(
		context.Background(), `title ~ "Board"`, []int{workspaceOne, workspaceTwo}, 7,
	)
	if err != nil {
		t.Fatalf("ListDistinctWorkspaceIDsWithQLContext: %v", err)
	}
	if len(workspaceIDs) != 2 || workspaceIDs[0] != workspaceOne || workspaceIDs[1] != workspaceTwo {
		t.Fatalf("workspace IDs = %v, want [%d %d]", workspaceIDs, workspaceOne, workspaceTwo)
	}
}

func newBoardConfigurationDataDB(t *testing.T, name string) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}

func boardConfigurationInsertID(t *testing.T, db database.Database) func(string, string, ...interface{}) int {
	t.Helper()
	return func(label, query string, args ...interface{}) int {
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
}
