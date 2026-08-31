package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/services"
)

func TestTestRunDetailReturnsCompleteWorkspaceScopedReadModel(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "test-run-detail.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	workspaceID := insertID(
		"workspace",
		`INSERT INTO workspaces (name, key) VALUES ('Test Run Detail', 'TRD') RETURNING id`,
	)
	otherWorkspaceID := insertID(
		"other workspace",
		`INSERT INTO workspaces (name, key) VALUES ('Other Workspace', 'OTH') RETURNING id`,
	)
	setID := insertID(
		"test set",
		`INSERT INTO test_sets (workspace_id, name) VALUES (?, 'Detail Set') RETURNING id`,
		workspaceID,
	)
	caseOneID := insertID(
		"first test case",
		`INSERT INTO test_cases (workspace_id, title, name, priority) VALUES (?, 'First case', 'First', 'high') RETURNING id`,
		workspaceID,
	)
	caseTwoID := insertID(
		"second test case",
		`INSERT INTO test_cases (workspace_id, title, name) VALUES (?, 'Second case', 'Second') RETURNING id`,
		workspaceID,
	)
	stepOneID := insertID(
		"first test step",
		`INSERT INTO test_steps (test_case_id, step_number, action, data, expected) VALUES (?, 1, 'Open form', '', 'Form opens') RETURNING id`,
		caseOneID,
	)
	stepTwoID := insertID(
		"second test step",
		`INSERT INTO test_steps (test_case_id, step_number, action, data, expected) VALUES (?, 2, 'Submit form', 'valid values', 'Success') RETURNING id`,
		caseOneID,
	)
	stepThreeID := insertID(
		"third test step",
		`INSERT INTO test_steps (test_case_id, step_number, action, data, expected) VALUES (?, 1, 'Review result', '', 'Result shown') RETURNING id`,
		caseTwoID,
	)
	runID := insertID(
		"test run",
		`INSERT INTO test_runs (workspace_id, set_id, name) VALUES (?, ?, 'Complete detail run') RETURNING id`,
		workspaceID,
		setID,
	)
	resultOneID := insertID(
		"first test result",
		`INSERT INTO test_results (run_id, test_case_id, status, actual_result, notes) VALUES (?, ?, 'failed', 'Validation error', 'Needs work') RETURNING id`,
		runID,
		caseOneID,
	)
	resultTwoID := insertID(
		"second test result",
		`INSERT INTO test_results (run_id, test_case_id, status) VALUES (?, ?, 'not_run') RETURNING id`,
		runID,
		caseTwoID,
	)
	if _, err := db.ExecWrite(`
		INSERT INTO test_step_results
			(test_result_id, test_step_id, status, actual_result, notes)
		VALUES (?, ?, 'failed', 'Server rejected request', 'Capture logs')
	`, resultOneID, stepTwoID); err != nil {
		t.Fatalf("insert step result: %v", err)
	}

	handler := NewTestRunHandlerWithPool(
		services.NewTestRunService(db),
		nil,
	)
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/workspaces/%d/test-runs/%d/detail", workspaceID, runID), nil)
	request.SetPathValue("workspaceId", fmt.Sprintf("%d", workspaceID))
	request.SetPathValue("id", fmt.Sprintf("%d", runID))
	recorder := httptest.NewRecorder()

	handler.GetDetail(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response TestRunDetailResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Run == nil || response.Run.ID != runID || response.Run.WorkspaceID != workspaceID {
		t.Fatalf("run = %+v, want run %d in workspace %d", response.Run, runID, workspaceID)
	}
	if len(response.TestCases) != 2 {
		t.Fatalf("test cases = %+v, want two cases", response.TestCases)
	}
	if response.TestCases[0].ID != caseOneID || len(response.TestCases[0].TestSteps) != 2 {
		t.Fatalf("first test case = %+v, want case %d with two steps", response.TestCases[0], caseOneID)
	}
	if response.TestCases[0].TestSteps[0].ID != stepOneID || response.TestCases[0].TestSteps[1].ID != stepTwoID {
		t.Fatalf("first case steps = %+v, want steps %d and %d", response.TestCases[0].TestSteps, stepOneID, stepTwoID)
	}
	if response.TestCases[1].ID != caseTwoID || len(response.TestCases[1].TestSteps) != 1 || response.TestCases[1].TestSteps[0].ID != stepThreeID {
		t.Fatalf("second test case = %+v, want case %d with step %d", response.TestCases[1], caseTwoID, stepThreeID)
	}
	if len(response.Results) != 2 || response.Results[0].ID != resultOneID || response.Results[1].ID != resultTwoID {
		t.Fatalf("results = %+v, want result rows %d and %d", response.Results, resultOneID, resultTwoID)
	}
	if response.Results[0].TestCaseTitle != "First case" {
		t.Fatalf("first result title = %q, want First case", response.Results[0].TestCaseTitle)
	}
	stepResult, ok := response.StepResults[fmt.Sprintf("%d_%d", caseOneID, stepTwoID)]
	if !ok || stepResult.Status != "failed" || stepResult.ActualResult != "Server rejected request" {
		t.Fatalf("step result = %+v, %t; want failed result", stepResult, ok)
	}

	otherRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/workspaces/%d/test-runs/%d/detail", otherWorkspaceID, runID), nil)
	otherRequest.SetPathValue("workspaceId", fmt.Sprintf("%d", otherWorkspaceID))
	otherRequest.SetPathValue("id", fmt.Sprintf("%d", runID))
	otherRecorder := httptest.NewRecorder()
	handler.GetDetail(otherRecorder, otherRequest)
	if otherRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status = %d, want 404; body=%s", otherRecorder.Code, otherRecorder.Body.String())
	}
}
