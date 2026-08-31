//go:build test

package services

import (
	"errors"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type testRunExecutionFixture struct {
	service              *TestRunService
	db                   *testutils.TestDB
	workspaceID, runID   int
	resultID             int
	stepOneID, stepTwoID int
	foreignItemID        int
}

func newTestRunExecutionFixture(t *testing.T) testRunExecutionFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	seedStatements := []string{
		`INSERT INTO test_cases (id, workspace_id, title, priority) VALUES (1101, 1, 'Execution case', 'medium')`,
		`INSERT INTO test_sets (id, workspace_id, name, description) VALUES (1201, 1, 'Execution set', '')`,
		`INSERT INTO set_test_cases (set_id, test_case_id) VALUES (1201, 1101)`,
		`INSERT INTO test_runs (id, workspace_id, set_id, name) VALUES (1301, 1, 1201, 'Execution run')`,
		`INSERT INTO test_results (id, run_id, test_case_id, status) VALUES (1401, 1301, 1101, 'not_run')`,
		`INSERT INTO test_steps (id, test_case_id, step_number, action, expected) VALUES (1501, 1101, 1, 'First action', 'First result')`,
		`INSERT INTO test_steps (id, test_case_id, step_number, action, expected) VALUES (1502, 1101, 2, 'Second action', 'Second result')`,
	}
	for _, statement := range seedStatements {
		if _, err := tdb.Exec(statement); err != nil {
			t.Fatalf("seed test-run execution fixture: %v", err)
		}
	}

	if _, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, active)
		VALUES (2, 'Foreign workspace', 'FOREIGN', true)
	`); err != nil {
		t.Fatalf("seed foreign workspace: %v", err)
	}
	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin foreign item transaction: %v", err)
	}
	statusID, priorityID, creatorID := data.StatusID, data.PriorityID, data.UserID
	foreignItemID, err := repository.NewItemRepository(tdb.GetDatabase()).Create(tx, &models.Item{
		WorkspaceID: 2, WorkspaceItemNumber: 1, Title: "Foreign item",
		StatusID: &statusID, PriorityID: &priorityID, CreatorID: &creatorID,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create foreign item: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit foreign item: %v", err)
	}

	return testRunExecutionFixture{
		service: NewTestRunService(tdb.GetDatabase()), db: tdb,
		workspaceID: data.WorkspaceID, runID: 1301, resultID: 1401,
		stepOneID: 1501, stepTwoID: 1502, foreignItemID: foreignItemID,
	}
}

func TestTestRunService_UpdateResultSanitizesAndScopesMutation(t *testing.T) {
	f := newTestRunExecutionFixture(t)

	updated, err := f.service.UpdateResult(f.workspaceID, f.runID, f.resultID, TestResultUpdateRequest{
		Status: "failed", ActualResult: "before<script>bad()</script><br/>after",
		Notes: "<img src=x onerror=alert(1)>note",
	})
	if err != nil {
		t.Fatalf("update result: %v", err)
	}
	if updated.Status != "failed" || updated.ExecutedAt == nil {
		t.Fatalf("updated result = %+v, want failed with execution time", updated)
	}
	if strings.Contains(updated.ActualResult, "<script>") || !strings.Contains(updated.ActualResult, "<br />") {
		t.Fatalf("actual result was not sanitized with rich-text policy: %q", updated.ActualResult)
	}
	if strings.Contains(strings.ToLower(updated.Notes), "onerror") {
		t.Fatalf("notes retained an event handler: %q", updated.Notes)
	}

	if _, err := f.service.UpdateResult(f.workspaceID+1, f.runID, f.resultID, TestResultUpdateRequest{Status: "passed"}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("cross-workspace update error = %v, want repository.ErrNotFound", err)
	}
	if _, err := f.service.UpdateResult(f.workspaceID, f.runID, f.resultID, TestResultUpdateRequest{Status: "invented"}); err == nil {
		t.Fatal("invalid status update succeeded")
	}
}

func TestTestRunService_UpdateStepResultOwnsValidationSanitizationAndCaseStatus(t *testing.T) {
	f := newTestRunExecutionFixture(t)

	if err := f.service.UpdateStepResult(f.workspaceID, f.runID, f.stepOneID, TestStepResultUpdateRequest{Status: "invented"}); err == nil {
		t.Fatal("invalid step status succeeded")
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM test_step_results`).Scan(&count); err != nil {
		t.Fatalf("count step results: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid update persisted %d step results, want 0", count)
	}

	if err := f.service.UpdateStepResult(f.workspaceID, f.runID, f.stepOneID, TestStepResultUpdateRequest{
		Status: "skipped", ActualResult: "before<script>bad()</script><br/>after",
		Notes: "<img src=x onerror=alert(1)>note",
	}); err != nil {
		t.Fatalf("create first step result: %v", err)
	}
	if err := f.service.UpdateStepResult(f.workspaceID, f.runID, f.stepTwoID, TestStepResultUpdateRequest{Status: "blocked"}); err != nil {
		t.Fatalf("create second step result: %v", err)
	}
	assertTestResultStatus(t, f, "blocked")

	if err := f.service.UpdateStepResult(f.workspaceID, f.runID, f.stepOneID, TestStepResultUpdateRequest{Status: "failed"}); err != nil {
		t.Fatalf("update first step result: %v", err)
	}
	assertTestResultStatus(t, f, "failed")

	for _, stepID := range []int{f.stepOneID, f.stepTwoID} {
		if err := f.service.UpdateStepResult(f.workspaceID, f.runID, stepID, TestStepResultUpdateRequest{Status: "passed"}); err != nil {
			t.Fatalf("mark step %d passed: %v", stepID, err)
		}
	}
	assertTestResultStatus(t, f, "passed")

	stepResults, err := f.service.ListStepResults(f.runID, f.workspaceID)
	if err != nil {
		t.Fatalf("list step results: %v", err)
	}
	if len(stepResults) != 2 || stepResults["1101_1501"].Status != "passed" || stepResults["1101_1502"].Status != "passed" {
		t.Fatalf("step result map = %+v", stepResults)
	}

	if err := f.service.UpdateStepResult(f.workspaceID, f.runID, f.stepOneID, TestStepResultUpdateRequest{
		Status: "passed", ItemID: &f.foreignItemID,
	}); !errors.Is(err, ErrTestRunItemNotFound) {
		t.Fatalf("foreign item error = %v, want ErrTestRunItemNotFound", err)
	}
}

func assertTestResultStatus(t *testing.T, f testRunExecutionFixture, want string) {
	t.Helper()
	var got string
	if err := f.db.QueryRow(`SELECT status FROM test_results WHERE id = ?`, f.resultID).Scan(&got); err != nil {
		t.Fatalf("read parent test result status: %v", err)
	}
	if got != want {
		t.Fatalf("parent test result status = %q, want %q", got, want)
	}
}
