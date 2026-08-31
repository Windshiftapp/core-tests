package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type workflowReadCountingDB struct {
	database.Database
	reads int
}

func (db *workflowReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestWorkflowListCanIncludeAllTransitionsWithTwoReads(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "workflows-with-transitions.db"))
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
	categoryID := insertID(
		"status category",
		`INSERT INTO status_categories (name, color) VALUES ('Bulk Workflow Category', '#123456')`,
	)
	statusOneID := insertID(
		"first status",
		`INSERT INTO statuses (name, description, category_id) VALUES ('Bulk Workflow Open', '', ?)`,
		categoryID,
	)
	statusTwoID := insertID(
		"second status",
		`INSERT INTO statuses (name, description, category_id) VALUES ('Bulk Workflow Done', '', ?)`,
		categoryID,
	)
	workflowOneID := insertID(
		"first workflow",
		`INSERT INTO workflows (name, description) VALUES ('Bulk Workflow One', '')`,
	)
	workflowTwoID := insertID(
		"second workflow",
		`INSERT INTO workflows (name, description) VALUES ('Bulk Workflow Two', '')`,
	)
	transitionOneID := insertID(
		"first transition",
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, ?, ?, 1)`,
		workflowOneID,
		statusOneID,
		statusTwoID,
	)
	transitionTwoID := insertID(
		"second transition",
		`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order) VALUES (?, NULL, ?, 1)`,
		workflowTwoID,
		statusOneID,
	)

	countingDB := &workflowReadCountingDB{Database: db}
	handler := NewWorkflowHandler(repository.NewWorkflowRepository(countingDB), nil)
	recorder := httptest.NewRecorder()
	handler.GetAll(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/workflows?include_transitions=true", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if countingDB.reads != 2 {
		t.Fatalf("read queries = %d, want 2 independent of workflow count", countingDB.reads)
	}
	var workflows []models.Workflow
	if err := json.NewDecoder(recorder.Body).Decode(&workflows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	findWorkflow := func(id int) *models.Workflow {
		t.Helper()
		for i := range workflows {
			if workflows[i].ID == id {
				return &workflows[i]
			}
		}
		return nil
	}
	first := findWorkflow(workflowOneID)
	if first == nil || len(first.Transitions) != 1 || first.Transitions[0].ID != transitionOneID {
		t.Fatalf("first workflow = %+v, want transition %d", first, transitionOneID)
	}
	if first.Transitions[0].FromStatusName != "Bulk Workflow Open" || first.Transitions[0].ToStatusName != "Bulk Workflow Done" {
		t.Fatalf("first transition = %+v, want joined status names", first.Transitions[0])
	}
	second := findWorkflow(workflowTwoID)
	if second == nil || len(second.Transitions) != 1 || second.Transitions[0].ID != transitionTwoID || second.Transitions[0].FromStatusID != nil {
		t.Fatalf("second workflow = %+v, want initial transition %d", second, transitionTwoID)
	}
}
